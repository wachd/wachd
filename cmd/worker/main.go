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

	// AI backend
	var aiEngine ai.Backend
	switch backend := os.Getenv("AI_BACKEND"); backend {
	case "claude":
		apiKey := os.Getenv("CLAUDE_API_KEY")
		model := os.Getenv("CLAUDE_MODEL")
		aiEngine = ai.NewClaudeBackend(apiKey, model)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ Claude backend configured (model: %s)", aiEngine.GetModelName())
		} else {
			log.Printf("⚠ Claude API key not configured (will skip AI analysis)")
		}

	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		model := os.Getenv("OPENAI_MODEL")
		aiEngine = ai.NewOpenAIBackend(apiKey, model)
		if aiEngine.IsAvailable(context.Background()) {
			log.Printf("✓ OpenAI backend configured (model: %s)", aiEngine.GetModelName())
		} else {
			log.Printf("⚠ OpenAI API key not configured (will skip AI analysis)")
		}

	default: // "ollama" or unset
		endpoint := os.Getenv("OLLAMA_ENDPOINT")
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		model := os.Getenv("OLLAMA_MODEL")
		if model == "" {
			model = "phi3"
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

		incidentMessage := ""
		if incident.Message != nil {
			incidentMessage = *incident.Message
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
			IncidentTitle:   incident.Title,
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

	// Notify on-call engineer via all configured channels
	if onCallUser != nil {
		w.sendNotifications(ctx, incident, onCallUser, aiAnalysis)
	}

	log.Printf("✓ Incident %s processed", incident.ID)
	return nil
}

// sendNotifications fans out to every configured notification channel.
// Each channel is attempted independently — a failure in one does not block others.
func (w *Worker) sendNotifications(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) {
	if w.slackNotifier != nil {
		if err := w.slackNotifier.SendIncidentAlert(ctx, incident, onCallUser, analysis); err != nil {
			log.Printf("Slack notification failed: %v", err)
		} else {
			log.Printf("  ✓ Slack")
		}
	}

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
