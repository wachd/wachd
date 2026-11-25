// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/notify"
	"github.com/wachd/wachd/internal/oncall"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/sanitiser"
	"github.com/wachd/wachd/internal/store"
)

// Worker processes alert jobs from the Redis queue.
// Data source config (GitHub, Loki, Prometheus) is loaded per-team from the
// database at job-processing time — not from global environment variables.
type Worker struct {
	db            *store.DB
	queue         *queue.Queue
	enc           *auth.Encryptor // decrypts per-team secrets stored in team_config
	oncallManager *oncall.Manager
	slackNotifier *notify.SlackNotifier
	emailNotifier *notify.EmailNotifier
	smsNotifier   *notify.SMSNotifier
	voiceNotifier *notify.VoiceNotifier
	relayNotifier *notify.RelayNotifier
	apnsNotifier  *notify.APNsNotifier
	fcmNotifier   *notify.FCMNotifier
	sanitiser     *sanitiser.Sanitiser
	correlator    *correlator.Correlator
	graphStore    graph.Store
	aiBackend     ai.Backend
}

// alertJobConcurrency keeps investigation work from holding the Redis consumer
// hostage while the independent notification dispatcher delivers durable intents.
// It is deliberately bounded so a slow AI backend cannot create unbounded load.
const alertJobConcurrency = 4

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	// Database
	db, err := store.NewDB(databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("✓ Connected to database")

	if err := db.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("✓ Database schema up to date")

	// Redis queue
	q, err := queue.NewQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer func() { _ = q.Close() }()
	log.Println("✓ Connected to Redis")

	// Encryptor — used to decrypt per-team GitHub tokens and other secrets
	var enc *auth.Encryptor
	if encKey := os.Getenv("WACHD_ENCRYPTION_KEY"); encKey != "" {
		enc, err = auth.NewEncryptor(encKey)
		if err != nil {
			log.Fatalf("Failed to initialise encryptor: %v", err)
		}
		log.Println("✓ Encryptor ready (per-team secrets can be decrypted)")
	} else {
		log.Println("⚠ WACHD_ENCRYPTION_KEY not set — per-team GitHub/Loki tokens will not be decrypted")
	}

	// On-call manager
	oncallMgr := oncall.NewManager(db)

	// Global fallback Slack notifier (used when team_config has no Slack credentials)
	var slackNotifier *notify.SlackNotifier
	if slackWebhookURL := os.Getenv("SLACK_WEBHOOK_URL"); slackWebhookURL != "" {
		slackNotifier = notify.NewSlackNotifier(slackWebhookURL, os.Getenv("SLACK_CHANNEL"))
		log.Println("✓ Fallback Slack notifier configured")
	}

	// Global fallback email notifier
	var emailNotifier *notify.EmailNotifier
	if smtpHost := os.Getenv("SMTP_HOST"); smtpHost != "" {
		emailNotifier = notify.NewEmailNotifier(smtpHost, os.Getenv("SMTP_PORT"), os.Getenv("SMTP_FROM"), os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD"))
		log.Println("✓ Fallback email notifier configured")
	}

	// SMS notifier (Twilio)
	var smsNotifier *notify.SMSNotifier
	twilioSID := os.Getenv("TWILIO_ACCOUNT_SID")
	twilioToken := os.Getenv("TWILIO_AUTH_TOKEN")
	twilioFrom := os.Getenv("TWILIO_FROM_NUMBER")
	if twilioSID != "" && twilioToken != "" && twilioFrom != "" {
		smsNotifier = notify.NewSMSNotifier(twilioSID, twilioToken, twilioFrom)
		log.Println("✓ SMS notifier configured (Twilio)")
	}

	// Voice call notifier (Twilio)
	var voiceNotifier *notify.VoiceNotifier
	twimlURL := os.Getenv("TWILIO_TWIML_URL")
	if twilioSID != "" && twilioToken != "" && twilioFrom != "" && twimlURL != "" {
		voiceNotifier = notify.NewVoiceNotifier(twilioSID, twilioToken, twilioFrom, twimlURL)
		log.Println("✓ Voice call notifier configured (Twilio)")
	}

	// Push notifiers — relay takes priority over direct APNs/FCM.
	// Relay: self-hosted deployments that cannot hold APNs/FCM credentials directly.
	// Direct: cloud-managed Wachd where credentials are available.
	relayNotifier, err := notify.NewRelayNotifier()
	if err != nil {
		log.Fatalf("push relay config error: %v", err)
	}
	if relayNotifier != nil {
		log.Println("✓ Push relay notifier configured (push.wachd.io)")
	}

	// APNs push notifier (iOS) — used only when relay is not configured.
	apnsNotifier := notify.NewAPNsNotifier()
	if apnsNotifier != nil && relayNotifier == nil {
		log.Println("✓ APNs push notifier configured (direct)")
	}

	// FCM push notifier (Android) — used only when relay is not configured.
	fcmNotifier := notify.NewFCMNotifier()
	if fcmNotifier != nil && relayNotifier == nil {
		log.Println("✓ FCM push notifier configured (direct)")
	}

	// PII sanitiser + correlator
	san := sanitiser.NewSanitiser()
	cor := correlator.NewCorrelator()
	log.Printf("✓ PII sanitiser loaded with %d patterns", san.GetPatternCount())

	// AI backend — seed system_config from env vars on first startup, then load from DB.
	// This means: env vars configure the initial value; after that the superadmin
	// controls the backend via PUT /api/v1/admin/system/ai without redeploying.
	envBackend := os.Getenv("AI_BACKEND")
	if envBackend == "" {
		envBackend = "ollama"
	}
	var envModel *string
	switch envBackend {
	case "claude":
		m := os.Getenv("CLAUDE_MODEL")
		if m != "" {
			envModel = &m
		}
	case "openai":
		m := os.Getenv("OPENAI_MODEL")
		if m != "" {
			envModel = &m
		}
	case "gemini":
		m := os.Getenv("GEMINI_MODEL")
		if m != "" {
			envModel = &m
		}
	default: // ollama
		m := os.Getenv("OLLAMA_MODEL")
		if m == "" {
			m = "phi3"
		}
		envModel = &m
	}
	if err := db.SeedSystemConfig(context.Background(), envBackend, envModel); err != nil {
		log.Printf("Warning: failed to seed system_config: %v", err)
	}

	sc, err := db.GetSystemConfig(context.Background())
	if err != nil {
		log.Printf("Warning: failed to load system_config, falling back to env: %v", err)
		sc = &store.SystemConfig{AIBackend: envBackend, AIModel: envModel}
	}

	var aiEngine ai.Backend
	switch sc.AIBackend {
	case "claude":
		apiKey := os.Getenv("CLAUDE_API_KEY")
		model := ""
		if sc.AIModel != nil {
			model = *sc.AIModel
		}
		aiEngine = ai.NewClaudeBackend(apiKey, model)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ Claude backend configured (model: %s)", aiEngine.GetModelName())
		} else {
			log.Printf("⚠ Claude API key not configured (will skip AI analysis)")
		}

	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		baseURL := os.Getenv("OPENAI_BASE_URL")
		apiVersion := os.Getenv("OPENAI_API_VERSION")
		model := ""
		if sc.AIModel != nil {
			model = *sc.AIModel
		}
		aiEngine = ai.NewOpenAIBackend(apiKey, model, baseURL, apiVersion)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ OpenAI backend configured (model: %s)", aiEngine.GetModelName())
		} else {
			log.Printf("⚠ OpenAI API key not configured (will skip AI analysis)")
		}

	case "gemini":
		apiKey := os.Getenv("GEMINI_API_KEY")
		model := ""
		if sc.AIModel != nil {
			model = *sc.AIModel
		}
		aiEngine = ai.NewGeminiBackend(apiKey, model)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ Gemini backend configured (model: %s)", aiEngine.GetModelName())
		} else {
			log.Printf("⚠ Gemini API key not configured (will skip AI analysis)")
		}

	default: // "ollama" or unset
		endpoint := os.Getenv("OLLAMA_ENDPOINT")
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		model := "phi3"
		if sc.AIModel != nil {
			model = *sc.AIModel
		}
		aiEngine = ai.NewOllamaBackend(endpoint, model)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ Ollama backend available at %s (model: %s)", endpoint, model)
		} else {
			log.Printf("⚠ Ollama not available at %s (will skip AI analysis)", endpoint)
		}
	}

	worker := &Worker{
		db:            db,
		queue:         q,
		enc:           enc,
		oncallManager: oncallMgr,
		slackNotifier: slackNotifier,
		emailNotifier: emailNotifier,
		smsNotifier:   smsNotifier,
		voiceNotifier: voiceNotifier,
		relayNotifier: relayNotifier,
		apnsNotifier:  apnsNotifier,
		fcmNotifier:   fcmNotifier,
		sanitiser:     san,
		correlator:    cor,
		graphStore:    graph.NewPostgresStore(db.Pool()),
		aiBackend:     aiEngine,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	log.Println("🔄 Worker started, waiting for jobs...")

	for i := 0; i < alertJobConcurrency; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if err := worker.processNextJob(ctx); err != nil {
						log.Printf("Error processing job: %v", err)
					}
				}
			}
		}()
	}

	go worker.runEscalationLoop(ctx)
	log.Println("✓ Escalation loop started (30s poll)")

	go worker.runPendingNotificationsLoop(ctx)
	log.Println("✓ Pending notifications loop started (30s poll)")
	go worker.runNotificationPreparationLoop(ctx)
	log.Println("✓ Notification preparation loop started (1s poll)")

	<-quit
	log.Println("Shutting down worker...")
	cancel()
	time.Sleep(2 * time.Second)
	log.Println("Worker stopped")
}

func (w *Worker) processNextJob(ctx context.Context) error {
	job, err := w.queue.DequeueAlert(ctx, 5*time.Second)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	log.Printf("📥 Processing job %s (type: %s, incident: %s)", job.ID, job.Type, job.IncidentID)
	switch job.Type {
	case "alert":
		return w.processAlertJob(ctx, job)
	case "incident_resolved":
		return w.processResolvedIncidentJob(ctx, job)
	default:
		log.Printf("warn: unknown job type %q for incident %s", job.Type, job.IncidentID)
		return nil
	}
}

func (w *Worker) processAlertJob(ctx context.Context, job *queue.Job) error {

	incident, err := w.db.GetIncident(ctx, job.TeamID, job.IncidentID)
	if err != nil {
		log.Printf("Failed to get incident %s: %v", job.IncidentID, err)
		return err
	}
	if incident == nil {
		log.Printf("Incident %s not found (may have been deleted), skipping", job.IncidentID)
		return nil
	}

	log.Printf("✓ Incident: [%s] %s (team: %s)", incident.Severity, incident.Title, incident.TeamID)

	w.writePendingIncidentToGraph(ctx, incident)

	onCallUser, err := w.oncallManager.GetCurrentOnCall(ctx, incident.TeamID)
	if err != nil {
		log.Printf("  ⚠ No on-call person found: %v", err)
	} else if onCallUser == nil {
		log.Printf("  ⚠ No on-call schedule configured for team %s", incident.TeamID)
	} else {
		log.Printf("  On-call: %s (%s)", onCallUser.Name, onCallUser.Email)
	}

	// Collect, sanitize, correlate
	log.Printf("🔍 Collecting context from team data sources...")
	collectedCtx := w.collectContext(ctx, incident)

	log.Printf("🧹 Sanitizing PII...")
	sanitizedCtx := w.sanitizeContext(collectedCtx)

	timeline := w.correlator.BuildTimeline(incident.FiredAt, sanitizedCtx)
	for _, c := range timeline.Correlations {
		log.Printf("   📊 %s", c)
	}

	if err := w.updateIncidentContext(ctx, incident, sanitizedCtx, timeline); err != nil {
		log.Printf("Warning: failed to save context: %v", err)
	}

	// AI root cause analysis
	var aiAnalysis *ai.AnalysisResponse
	if w.aiBackend != nil && w.aiBackend.IsAvailable(ctx) {
		log.Printf("🤖 Running AI root cause analysis (%s)...", w.aiBackend.GetModelName())

		// Sanitise incident title and message before they reach the AI backend.
		incidentTitle := w.sanitiser.Sanitise(incident.Title)
		incidentMessage := ""
		if incident.Message != nil {
			incidentMessage = w.sanitiser.Sanitise(*incident.Message)
		}

		commitMsgs := make([]string, len(sanitizedCtx.Commits))
		for i, c := range sanitizedCtx.Commits {
			sha := c.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			commitMsgs[i] = formatCommitForPrompt(c, sha, incident.FiredAt)
			for _, change := range c.Changes {
				if change.Patch != "" {
					commitMsgs[i] += fmt.Sprintf("\n  FILE %s\n%s", change.Filename, change.Patch)
				}
			}
		}

		logMsgs := make([]string, len(sanitizedCtx.Logs))
		for i, l := range sanitizedCtx.Logs {
			logMsgs[i] = l.Message
		}

		prompt := ai.BuildPrompt(&ai.AnalysisRequest{
			IncidentTitle:   incidentTitle,
			IncidentMessage: incidentMessage,
			Severity:        incident.Severity,
			Commits:         commitMsgs,
			ErrorLogs:       logMsgs,
			MetricsSummary:  formatMetricsSummary(sanitizedCtx.Metrics),
			Timeline:        formatTimeline(timeline),
		})

		rawResp, err := w.aiBackend.Analyze(ctx, prompt)
		if err != nil {
			log.Printf("Warning: AI analysis failed: %v", err)
		} else {
			aiAnalysis = ai.ParseAnalysisResponse(rawResp)
			log.Printf("  Cause: %s", aiAnalysis.RootCause)
			log.Printf("  Action: %s", aiAnalysis.SuggestedAction)
			log.Printf("  Confidence: %s", aiAnalysis.Confidence)
			if err := w.updateIncidentAnalysis(ctx, incident, aiAnalysis); err != nil {
				log.Printf("Warning: failed to save AI analysis: %v", err)
			}
		}
	}

	// Initial notification intents were persisted before investigation. AI/RCA
	// output is intentionally not sent as a second initial notification.
	w.firePendingNotifications(ctx)

	log.Printf("✓ Incident %s processed", incident.ID)
	return nil
}

// runNotificationPreparationLoop expands the transactionally-created handoff
// into recipient/channel intents independently of investigation and AI work.
func (w *Worker) runNotificationPreparationLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		w.prepareNotificationIntents(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) prepareNotificationIntents(ctx context.Context) {
	items, err := w.db.ClaimNotificationPreparations(ctx, 100, 30*time.Second)
	if err != nil {
		log.Printf("notification preparation: query failed: %v", err)
		return
	}
	for _, item := range items {
		incident, err := w.db.GetIncident(ctx, item.TeamID, item.IncidentID)
		if err != nil || incident == nil || incident.Status != "open" {
			if err := w.db.MarkNotificationPreparationProcessed(ctx, item.IncidentID); err != nil {
				log.Printf("notification preparation finalize %s: %v", item.IncidentID, err)
			}
			continue
		}
		onCall, err := w.oncallManager.GetCurrentOnCall(ctx, incident.TeamID)
		if err != nil || onCall == nil {
			failure := "no on-call assignment"
			if err != nil {
				failure = sanitizeNotificationError(err)
			}
			log.Printf("notification preparation pending for %s: %s", incident.ID, failure)
			_ = w.db.MarkNotificationPreparationFailed(ctx, incident.ID, failure)
			continue
		}
		if err := w.sendNotifications(ctx, incident, onCall, nil, nil); err != nil {
			log.Printf("notification preparation retry for %s: %v", incident.ID, err)
			_ = w.db.MarkNotificationPreparationFailed(ctx, incident.ID, sanitizeNotificationError(err))
			continue
		}
		if err := w.queueTeamSlack(ctx, incident, onCall, nil, nil); err != nil {
			log.Printf("notification preparation retry for %s: %v", incident.ID, err)
			_ = w.db.MarkNotificationPreparationFailed(ctx, incident.ID, sanitizeNotificationError(err))
			continue
		}
		if err := w.db.MarkNotificationPreparationProcessed(ctx, incident.ID); err != nil {
			log.Printf("notification preparation finalize %s: %v", incident.ID, err)
		}
	}
}

func (w *Worker) processResolvedIncidentJob(ctx context.Context, job *queue.Job) error {
	incident, err := w.db.GetIncident(ctx, job.TeamID, job.IncidentID)
	if err != nil {
		log.Printf("warn: resolved graph write skipped, incident lookup failed for %s: %v", job.IncidentID, err)
		return nil
	}
	if incident == nil {
		log.Printf("Incident %s not found (may have been deleted), skipping", job.IncidentID)
		return nil
	}
	if incident.Status != "resolved" {
		log.Printf("warn: resolved graph write skipped for incident %s with status %q", incident.ID, incident.Status)
		return nil
	}

	if err := w.writeResolvedIncidentToGraph(ctx, incident); err != nil {
		log.Printf("warn: graph write failed for resolved incident %s: %v", incident.ID, err)
	}
	return nil
}

type notificationPayload struct {
	Analysis *ai.AnalysisResponse    `json:"analysis,omitempty"`
	Similar  *notify.SimilarIncident `json:"similar,omitempty"`
}

func marshalNotificationPayload(analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) []byte {
	b, err := json.Marshal(notificationPayload{Analysis: analysis, Similar: similar})
	if err != nil {
		return nil
	}
	return b
}

func (w *Worker) queueNotification(ctx context.Context, p *store.PendingNotification, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) error {
	p.Payload = marshalNotificationPayload(analysis, similar)
	if err := w.db.QueuePendingNotification(ctx, p); err != nil {
		log.Printf("warn: persist notification intent (channel=%s): %v", p.Channel, err)
		return err
	}
	log.Printf("  ⏳ %s notification persisted", p.Channel)
	return nil
}

// sendNotifications persists every configured personal-channel intent.
// If the user has notification rules configured, only those channels fire (with
// optional delay). If no rules exist the function falls back to firing all
// globally-configured channels immediately (preserving pre-rules behaviour).
func (w *Worker) sendNotifications(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) error {
	var firstErr error
	rules, err := w.db.GetUserNotificationRules(ctx, onCallUser.ID, onCallUser.Source, "new_alert")
	if err != nil {
		log.Printf("warn: load notification rules for user %s: %v — falling back to all channels", onCallUser.ID, err)
	}

	if len(rules) == 0 {
		// Preserve legacy channel selection while making delivery durable.
		for _, configured := range []struct {
			channel string
			enabled bool
		}{{"email", w.emailNotifier != nil}, {"sms", w.smsNotifier != nil}, {"voice", w.voiceNotifier != nil}} {
			if configured.enabled {
				if err := w.queueNotification(ctx, &store.PendingNotification{IncidentID: incident.ID, TeamID: incident.TeamID,
					UserID: onCallUser.ID, UserSource: onCallUser.Source, Channel: configured.channel,
					EventType: "new_alert", Phase: "initial", ScheduledAt: time.Now().UTC()}, analysis, similar); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		p := &store.PendingNotification{
			IncidentID:  incident.ID,
			TeamID:      incident.TeamID,
			UserID:      onCallUser.ID,
			UserSource:  onCallUser.Source,
			Channel:     rule.Channel,
			EventType:   rule.EventType,
			Phase:       "initial",
			ScheduledAt: time.Now().Add(time.Duration(rule.DelayMinutes) * time.Minute),
		}
		if err := w.queueNotification(ctx, p, analysis, similar); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Worker) teamSlackNotifier(ctx context.Context, teamID uuid.UUID) *notify.SlackNotifier {
	slackNotifier := w.slackNotifier
	if cfg, err := w.db.GetTeamConfig(ctx, teamID); err == nil && cfg != nil {
		if cfg.SlackWebhookURL != nil && *cfg.SlackWebhookURL != "" {
			channel := ""
			if cfg.SlackChannel != nil {
				channel = *cfg.SlackChannel
			}
			slackNotifier = notify.NewSlackNotifier(*cfg.SlackWebhookURL, channel)
		}
	}
	return slackNotifier
}

// queueTeamSlack persists one team-level Slack intent per incident.
func (w *Worker) queueTeamSlack(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) error {
	if w.teamSlackNotifier(ctx, incident.TeamID) == nil {
		return nil
	}
	userID, source := uuid.Nil, "team"
	if onCallUser != nil {
		userID, source = onCallUser.ID, onCallUser.Source
	}
	return w.queueNotification(ctx, &store.PendingNotification{IncidentID: incident.ID, TeamID: incident.TeamID,
		UserID: userID, UserSource: source, Channel: "slack", EventType: "new_alert",
		Phase: "initial", ScheduledAt: time.Now().UTC()}, analysis, similar)
}

func (w *Worker) fireTeamSlack(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) error {
	slackNotifier := w.teamSlackNotifier(ctx, incident.TeamID)
	if slackNotifier == nil {
		return errors.New("slack notifier not configured")
	}
	if err := slackNotifier.SendIncidentAlertWithSimilar(ctx, incident, onCallUser, analysis, similar); err != nil {
		return err
	}
	log.Printf("  ✓ Slack (team channel)")
	return nil
}

// fireAllChannels sends email/SMS/voice for a user — used as fallback when no
// notification rules are configured, and for escalation re-notifications.
// Slack is intentionally excluded: it fires once at incident creation only.
func (w *Worker) fireAllChannels(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) {
	if w.emailNotifier != nil {
		if err := w.emailNotifier.SendIncidentAlertWithSimilar(ctx, incident, onCallUser, analysis, similar); err != nil {
			log.Printf("Email notification failed: %v", err)
		} else {
			log.Printf("  ✓ Email")
		}
	}

	if w.smsNotifier != nil {
		if err := w.smsNotifier.SendIncidentAlertWithSimilar(ctx, incident, onCallUser, analysis, similar); err != nil {
			log.Printf("SMS notification failed: %v", err)
		} else {
			log.Printf("  ✓ SMS")
		}
	}

	if w.voiceNotifier != nil {
		if err := w.voiceNotifier.SendIncidentAlert(ctx, incident, onCallUser, analysis); err != nil {
			log.Printf("Voice call failed: %v", err)
		} else {
			log.Printf("  ✓ Voice call initiated")
		}
	}
}

func (w *Worker) deliverChannel(ctx context.Context, channel string, incident *store.Incident, user *store.TeamMember, analysis *ai.AnalysisResponse, similar *notify.SimilarIncident) error {
	switch channel {
	case "email":
		if w.emailNotifier == nil {
			return errors.New("email notifier not configured")
		}
		return w.emailNotifier.SendIncidentAlertWithSimilar(ctx, incident, user, analysis, similar)
	case "sms":
		if w.smsNotifier == nil {
			return errors.New("SMS notifier not configured")
		}
		return w.smsNotifier.SendIncidentAlertWithSimilar(ctx, incident, user, analysis, similar)
	case "voice":
		if w.voiceNotifier == nil {
			return errors.New("voice notifier not configured")
		}
		return w.voiceNotifier.SendIncidentAlert(ctx, incident, user, analysis)
	case "slack":
		return w.fireTeamSlack(ctx, incident, user, analysis, similar)
	case "push":
		tokens, err := w.db.GetPushTokensByUserID(ctx, user.ID, user.Source)
		if err != nil {
			return fmt.Errorf("load push tokens: %w", err)
		}
		if len(tokens) == 0 {
			return errors.New("no push tokens configured")
		}
		var iosTokens, androidTokens []string
		for _, t := range tokens {
			switch t.Platform {
			case "ios":
				iosTokens = append(iosTokens, t.Token)
			case "android":
				androidTokens = append(androidTokens, t.Token)
			}
		}
		incidentID := incident.ID.String()
		if w.relayNotifier != nil {
			// Relay mode — zero incident content sent; app fetches details from customer server.
			if len(iosTokens) > 0 {
				failed := w.relayNotifier.SendPush(ctx, "ios", iosTokens, incidentID)
				log.Printf("  ✓ push relay (ios): %d/%d devices", len(iosTokens)-len(failed), len(iosTokens))
				if len(failed) > 0 {
					return fmt.Errorf("push relay ios failed for %d devices", len(failed))
				}
			}
			if len(androidTokens) > 0 {
				failed := w.relayNotifier.SendPush(ctx, "android", androidTokens, incidentID)
				log.Printf("  ✓ push relay (android): %d/%d devices", len(androidTokens)-len(failed), len(androidTokens))
				if len(failed) > 0 {
					return fmt.Errorf("push relay android failed for %d devices", len(failed))
				}
			}
		} else {
			// Direct mode — cloud-managed Wachd with APNs/FCM credentials.
			// Include severity prefix in body since the payload is delivered as-is.
			body := incident.Title
			if incident.Severity != "" && incident.Severity != "unknown" {
				body = "[" + incident.Severity + "] " + incident.Title
			}
			if len(iosTokens) > 0 && w.apnsNotifier != nil {
				failed := w.apnsNotifier.SendIncidentPush(ctx, iosTokens, incident.ID, "New Incident", body)
				log.Printf("  ✓ APNs push: %d/%d devices", len(iosTokens)-len(failed), len(iosTokens))
				if len(failed) > 0 {
					return fmt.Errorf("APNs failed for %d devices", len(failed))
				}
			}
			if len(androidTokens) > 0 && w.fcmNotifier != nil {
				failed := w.fcmNotifier.SendIncidentPush(ctx, androidTokens, incident.ID, "New Incident", body)
				log.Printf("  ✓ FCM push: %d/%d devices", len(androidTokens)-len(failed), len(androidTokens))
				if len(failed) > 0 {
					return fmt.Errorf("FCM failed for %d devices", len(failed))
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown notification channel %q", channel)
	}
}

// runPendingNotificationsLoop polls frequently enough for newly prepared
// initial alerts to be independent of investigation/AI completion. Claims are
// still protected by PostgreSQL leases, so multiple replicas remain safe.
func (w *Worker) runPendingNotificationsLoop(ctx context.Context) {
	interval := time.Second
	if raw := os.Getenv("WACHD_NOTIFICATION_POLL_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 100*time.Millisecond {
			interval = parsed
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.firePendingNotifications(ctx)
		}
	}
}

func (w *Worker) firePendingNotifications(ctx context.Context) {
	pending, err := w.db.ClaimDuePendingNotifications(ctx, 100, 2*time.Minute)
	if err != nil {
		log.Printf("pending notifications: query failed: %v", err)
		return
	}
	for _, p := range pending {
		if p.ClaimToken == nil {
			continue
		}
		// Acknowledgement or resolution cancels an outstanding initial alert.
		incident, err := w.db.GetIncident(ctx, p.TeamID, p.IncidentID)
		if err != nil || incident == nil || incident.Status != "open" {
			_ = w.db.CancelPendingNotificationsForIncident(ctx, p.IncidentID)
			continue
		}
		user, err := w.db.GetMemberByID(ctx, p.UserID)
		if p.Channel != "slack" && (err != nil || user == nil) {
			log.Printf("pending notification %s: user %s not found — skipping", p.ID, p.UserID)
			_ = w.db.MarkPendingNotificationFailed(ctx, p.ID, *p.ClaimToken, p.NextAttemptAt, "recipient not found", true)
			continue
		}
		var payload notificationPayload
		if len(p.Payload) > 0 {
			_ = json.Unmarshal(p.Payload, &payload)
		}
		recipient := "team"
		if user != nil {
			recipient = user.Name
		}
		log.Printf("⏰ Firing %s for incident %s (recipient: %s)", p.Channel, p.IncidentID, recipient)
		deliveryErr := w.deliverChannel(ctx, p.Channel, incident, user, payload.Analysis, payload.Similar)
		if deliveryErr == nil {
			if err := w.db.MarkPendingNotificationSent(ctx, p.ID, *p.ClaimToken); err != nil {
				log.Printf("notification %s delivered but acknowledgement failed: %v", p.ID, err)
			}
			continue
		}
		failure := sanitizeNotificationError(deliveryErr)
		permanent := isPermanentNotificationError(deliveryErr)
		retryAt := time.Now().UTC().Add(notificationBackoff(p.AttemptCount))
		if err := w.db.MarkPendingNotificationFailed(ctx, p.ID, *p.ClaimToken, retryAt, failure, permanent); err != nil {
			log.Printf("notification %s failure recording failed: %v", p.ID, err)
		}
	}
}

func notificationBackoff(attempt int) time.Duration {
	delays := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	if attempt <= 0 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

var notificationSecretPattern = regexp.MustCompile(`(?i)(password|passwd|token|secret|authorization)([=: ]+)([^ ,;]+)`)

func sanitizeNotificationError(err error) string {
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	msg = notificationSecretPattern.ReplaceAllString(msg, "$1$2[REDACTED]")
	if len(msg) > 512 {
		msg = msg[:512]
	}
	return msg
}

func isPermanentNotificationError(err error) bool {
	var smtpErr *textproto.Error
	if errors.As(err, &smtpErr) {
		return smtpErr.Code >= 500 && smtpErr.Code < 600
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not configured") || strings.Contains(msg, "no push tokens") ||
		strings.Contains(msg, "recipient not found") || strings.Contains(msg, "unknown notification channel")
}
