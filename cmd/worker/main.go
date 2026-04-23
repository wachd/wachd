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
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/correlator"
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
	sanitiser     *sanitiser.Sanitiser
	correlator    *correlator.Correlator
	aiBackend     ai.Backend
}

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
		model := ""
		if sc.AIModel != nil {
			model = *sc.AIModel
		}
		aiEngine = ai.NewOpenAIBackend(apiKey, model)
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
		sanitiser:     san,
		correlator:    cor,
		aiBackend:     aiEngine,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	log.Println("🔄 Worker started, waiting for jobs...")

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

	go worker.runEscalationLoop(ctx)
	log.Println("✓ Escalation loop started (30s poll)")

	go worker.runPendingNotificationsLoop(ctx)
	log.Println("✓ Pending notifications loop started (30s poll)")

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

	incident, err := w.db.GetIncident(ctx, job.TeamID, job.IncidentID)
	if err != nil {
		log.Printf("Failed to get incident %s: %v", job.IncidentID, err)
		return err
	}

	log.Printf("✓ Incident: [%s] %s (team: %s)", incident.Severity, incident.Title, incident.TeamID)

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
			commitMsgs[i] = fmt.Sprintf("[%s] %s by %s", sha, c.Message, c.Author)
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

	// Notify on-call engineer via their personal notification rules (email/SMS/voice).
	// Slack fires separately below — once per incident, team-level, not per-user.
	if onCallUser != nil {
		w.sendNotifications(ctx, incident, onCallUser, aiAnalysis)
	}

	// Fire team Slack channel once — one post per incident, keeps the channel clean.
	w.fireTeamSlack(ctx, incident, onCallUser, aiAnalysis)

	log.Printf("✓ Incident %s processed", incident.ID)
	return nil
}

// sendNotifications fans out to every configured notification channel.
// If the user has notification rules configured, only those channels fire (with
// optional delay). If no rules exist the function falls back to firing all
// globally-configured channels immediately (preserving pre-rules behaviour).
func (w *Worker) sendNotifications(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) {
	rules, err := w.db.GetUserNotificationRules(ctx, onCallUser.ID, onCallUser.Source, "new_alert")
	if err != nil {
		log.Printf("warn: load notification rules for user %s: %v — falling back to all channels", onCallUser.ID, err)
	}

	if len(rules) == 0 {
		// No rules configured — fire all available channels immediately (legacy behaviour).
		w.fireAllChannels(ctx, incident, onCallUser, analysis)
		return
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.DelayMinutes == 0 {
			w.fireChannel(ctx, rule.Channel, incident, onCallUser, analysis)
		} else {
			p := &store.PendingNotification{
				IncidentID:  incident.ID,
				TeamID:      incident.TeamID,
				UserID:      onCallUser.ID,
				UserSource:  onCallUser.Source,
				Channel:     rule.Channel,
				ScheduledAt: time.Now().Add(time.Duration(rule.DelayMinutes) * time.Minute),
			}
			if qErr := w.db.QueuePendingNotification(ctx, p); qErr != nil {
				log.Printf("warn: queue pending notification (channel=%s delay=%dm): %v", rule.Channel, rule.DelayMinutes, qErr)
			} else {
				log.Printf("  ⏳ %s queued (+%dm)", rule.Channel, rule.DelayMinutes)
			}
		}
	}
}

// fireTeamSlack posts one notification to the team Slack channel.
// Called once when the incident is first created — not on escalation steps.
func (w *Worker) fireTeamSlack(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) {
	slackNotifier := w.slackNotifier
	if cfg, err := w.db.GetTeamConfig(ctx, incident.TeamID); err == nil && cfg != nil {
		if cfg.SlackWebhookURL != nil && *cfg.SlackWebhookURL != "" {
			channel := ""
			if cfg.SlackChannel != nil {
				channel = *cfg.SlackChannel
			}
			slackNotifier = notify.NewSlackNotifier(*cfg.SlackWebhookURL, channel)
		}
	}
	if slackNotifier == nil {
		return
	}
	if err := slackNotifier.SendIncidentAlert(ctx, incident, onCallUser, analysis); err != nil {
		log.Printf("Slack notification failed: %v", err)
	} else {
		log.Printf("  ✓ Slack (team channel)")
	}
}

// fireAllChannels sends email/SMS/voice for a user — used as fallback when no
// notification rules are configured, and for escalation re-notifications.
// Slack is intentionally excluded: it fires once at incident creation only.
func (w *Worker) fireAllChannels(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) {
	if w.emailNotifier != nil {
		if err := w.emailNotifier.SendIncidentAlert(ctx, incident, onCallUser, analysis); err != nil {
			log.Printf("Email notification failed: %v", err)
		} else {
			log.Printf("  ✓ Email")
		}
	}

	if w.smsNotifier != nil {
		if err := w.smsNotifier.SendIncidentAlert(ctx, incident, onCallUser, analysis); err != nil {
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

// fireChannel sends a single notification channel for a user.
// Slack is not handled here — it fires once at incident creation via fireTeamSlack.
func (w *Worker) fireChannel(ctx context.Context, channel string, incident *store.Incident, user *store.TeamMember, analysis *ai.AnalysisResponse) {
	switch channel {
	case "email":
		if w.emailNotifier != nil {
			if err := w.emailNotifier.SendIncidentAlert(ctx, incident, user, analysis); err != nil {
				log.Printf("Email notification failed: %v", err)
			} else {
				log.Printf("  ✓ Email")
			}
		}
	case "sms":
		if w.smsNotifier != nil {
			if err := w.smsNotifier.SendIncidentAlert(ctx, incident, user, analysis); err != nil {
				log.Printf("SMS notification failed: %v", err)
			} else {
				log.Printf("  ✓ SMS")
			}
		}
	case "voice":
		if w.voiceNotifier != nil {
			if err := w.voiceNotifier.SendIncidentAlert(ctx, incident, user, analysis); err != nil {
				log.Printf("Voice call failed: %v", err)
			} else {
				log.Printf("  ✓ Voice call initiated")
			}
		}
	default:
		log.Printf("warn: unknown notification channel %q — skipping", channel)
	}
}

// runPendingNotificationsLoop polls every 30s for delayed notifications that are due.
// A random startup jitter (0–10s) staggers multiple worker replicas.
func (w *Worker) runPendingNotificationsLoop(ctx context.Context) {
	jitter := time.Duration(rand.Int63n(int64(10 * time.Second))) //nolint:gosec
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}
	ticker := time.NewTicker(30 * time.Second)
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
	pending, err := w.db.GetDuePendingNotifications(ctx)
	if err != nil {
		log.Printf("pending notifications: query failed: %v", err)
		return
	}
	for _, p := range pending {
		// Only fire if the incident is still open and unacknowledged.
		incident, err := w.db.GetIncident(ctx, p.TeamID, p.IncidentID)
		if err != nil || incident == nil || incident.Status != "open" {
			_ = w.db.MarkPendingNotificationSent(ctx, p.ID) // mark consumed so it doesn't loop
			continue
		}
		user, err := w.db.GetMemberByID(ctx, p.UserID)
		if err != nil || user == nil {
			log.Printf("pending notification %s: user %s not found — skipping", p.ID, p.UserID)
			_ = w.db.MarkPendingNotificationSent(ctx, p.ID)
			continue
		}
		log.Printf("⏰ Firing delayed %s for incident %s (user: %s)", p.Channel, p.IncidentID, user.Name)
		w.fireChannel(ctx, p.Channel, incident, user, nil) // no AI analysis for delayed sends
		_ = w.db.MarkPendingNotificationSent(ctx, p.ID)
	}
}
