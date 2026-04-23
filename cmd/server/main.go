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
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/license"
	"github.com/wachd/wachd/internal/notify"
	"github.com/wachd/wachd/internal/oncall"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/store"
	"github.com/wachd/wachd/internal/validate"
)

type Server struct {
	cfg            store.ConfigStore
	db             *store.DB
	queue          *queue.Queue
	oncallManager  *oncall.Manager
	sessions       *auth.SessionStore
	license        *license.License
	enc            *auth.Encryptor
	port           string
	webhookLimiter *webhookIPLimiter
}

// webhookIPLimiter enforces per-IP rate limits on the webhook endpoint.
// Each IP gets a token bucket: burst of 20, refill at 1 token/second (60/min).
// Limiters not seen for >10 minutes are pruned to prevent memory growth.
type webhookIPLimiter struct {
	mu      sync.Mutex
	clients map[string]*webhookClient
}

type webhookClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newWebhookIPLimiter() *webhookIPLimiter {
	l := &webhookIPLimiter{clients: make(map[string]*webhookClient)}
	go l.cleanup()
	return l
}

func (l *webhookIPLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.clients[ip]
	if !ok {
		c = &webhookClient{limiter: rate.NewLimiter(rate.Every(time.Second), 20)}
		l.clients[ip] = c
	}
	c.lastSeen = time.Now()
	return c.limiter.Allow()
}

func (l *webhookIPLimiter) cleanup() {
	for range time.Tick(5 * time.Minute) {
		l.mu.Lock()
		for ip, c := range l.clients {
			if time.Since(c.lastSeen) > 10*time.Minute {
				delete(l.clients, ip)
			}
		}
		l.mu.Unlock()
	}
}

// GrafanaWebhook represents a simplified Grafana webhook payload
type GrafanaWebhook struct {
	Title       string                   `json:"title"`
	RuleName    string                   `json:"ruleName"`
	State       string                   `json:"state"`
	Message     string                   `json:"message"`
	Tags        map[string]string        `json:"tags"`
	EvalMatches []map[string]interface{} `json:"evalMatches"`
}

// DatadogWebhook represents a Datadog webhook alert payload.
// See: https://docs.datadoghq.com/integrations/webhooks/
type DatadogWebhook struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Body             string `json:"body"`
	AlertType        string `json:"alert_type"`        // error | warning | info | success
	AlertTransition  string `json:"alert_transition"`  // Triggered | Recovered | Re-Triggered | Resolved
	Priority         string `json:"priority"`          // normal | low
	Hostname         string `json:"hostname"`
	Tags             string `json:"tags"`
	OrgID            int64  `json:"org_id"`
	AlertID          int64  `json:"alert_id"`
}

// parseWebhookPayload detects the source format and extracts a normalised
// (title, message, severity, source) tuple. Unknown payloads fall back to a
// generic extraction using the raw JSON keys "title" and "message".
func parseWebhookPayload(body []byte) (title, message, severity, source string) {
	// Try to detect Datadog: it always has alert_id (int) or alert_transition field.
	var dd DatadogWebhook
	if err := json.Unmarshal(body, &dd); err == nil && (dd.AlertID != 0 || dd.AlertTransition != "") {
		sev := "unknown"
		switch dd.AlertType {
		case "error":
			sev = "critical"
		case "warning":
			sev = "high"
		case "info":
			sev = "low"
		case "success":
			sev = "info"
		}
		msg := dd.Body
		if dd.Hostname != "" {
			msg += " (host: " + dd.Hostname + ")"
		}
		t := dd.Title
		if t == "" {
			t = "Datadog alert"
		}
		return t, msg, sev, "datadog"
	}

	// Try Grafana
	var gf GrafanaWebhook
	if err := json.Unmarshal(body, &gf); err == nil && (gf.Title != "" || gf.RuleName != "") {
		t := gf.Title
		if t == "" {
			t = gf.RuleName
		}
		sev := "unknown"
		switch gf.State {
		case "alerting":
			sev = "high"
		case "ok":
			sev = "low"
		}
		return t, gf.Message, sev, "grafana"
	}

	// Generic fallback — extract title/message from any JSON payload
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err == nil {
		t, _ := raw["title"].(string)
		if t == "" {
			t, _ = raw["name"].(string)
		}
		if t == "" {
			t = "Alert"
		}
		msg, _ := raw["message"].(string)
		if msg == "" {
			msg, _ = raw["description"].(string)
		}
		sev, _ := raw["severity"].(string)
		if sev == "" {
			sev = "unknown"
		}
		return t, msg, sev, "generic"
	}

	return "Alert", "", "unknown", "generic"
}

func main() {
	// Load environment variables
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	authDisabled := os.Getenv("AUTH_DISABLED") == "true"

	// Require encryption key unless running in dev mode (AUTH_DISABLED)
	encryptionKey := os.Getenv("WACHD_ENCRYPTION_KEY")
	if encryptionKey == "" && !authDisabled {
		log.Fatal("WACHD_ENCRYPTION_KEY is required (32-byte hex, 64 chars). " +
			"Generate with: openssl rand -hex 32")
	}

	var enc *auth.Encryptor
	if encryptionKey != "" {
		var err error
		enc, err = auth.NewEncryptor(encryptionKey)
		if err != nil {
			log.Fatalf("Invalid WACHD_ENCRYPTION_KEY: %v", err)
		}
	}

	// Initialize database
	db, err := store.NewDB(databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("✓ Connected to database")

	// Run schema migrations (idempotent — safe on every startup)
	if err := db.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("✓ Database schema up to date")

	// First-run bootstrap: create a default team if none exist
	if err := bootstrapFirstTeam(db); err != nil {
		log.Printf("Warning: bootstrap check failed: %v", err)
	}

	// Initialize queue (also used for session state storage)
	q, err := queue.NewQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer func() { _ = q.Close() }()
	log.Println("✓ Connected to Redis")

	// Build a dedicated Redis client for auth (sessions, oauth state)
	redisOpt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL for auth: %v", err)
	}
	redisClient := redis.NewClient(redisOpt)
	defer func() { _ = redisClient.Close() }()

	sessions := auth.NewSessionStore(redisClient)

	// Load license key — falls back to OSS limits if unset or invalid.
	lic, licErr := license.Load(os.Getenv("WACHD_LICENSE_KEY"))
	if licErr != nil {
		log.Printf("⚠ License key rejected (%v) — running under OSS limits (1 team, 5 users)", licErr)
	} else if lic.IsPaid() {
		log.Printf("✓ License: %s tier — customer: %s", lic.Tier, lic.CustomerName)
		if lic.IsGracePeriod {
			log.Printf("⚠ License is in grace period — expires %s — renew at wachd.io", lic.ExpiresAt.Format("2006-01-02"))
		}
	} else {
		log.Printf("✓ License: open-source tier (maxTeams=%d maxUsers=%d maxAlerts/month=%d)",
			lic.MaxTeams, lic.MaxUsers, lic.MaxAlertsMonth)
	}

	// Initialize on-call manager
	oncallMgr := oncall.NewManager(db)

	// Create server
	server := &Server{
		cfg:            db,
		db:             db,
		queue:          q,
		oncallManager:  oncallMgr,
		sessions:       sessions,
		license:        lic,
		enc:            enc,
		port:           port,
		webhookLimiter: newWebhookIPLimiter(),
	}

	// Set up HTTP router
	router := mux.NewRouter()

	// --- Auth routes (always public) ---
	if !authDisabled {
		tenantID := os.Getenv("ENTRA_TENANT_ID")
		clientID := os.Getenv("ENTRA_CLIENT_ID")
		clientSecret := os.Getenv("ENTRA_CLIENT_SECRET")
		redirectURI := os.Getenv("AUTH_REDIRECT_URI")
		if redirectURI == "" {
			if fe := os.Getenv("FRONTEND_URL"); fe != "" {
				redirectURI = strings.TrimRight(fe, "/") + "/auth/callback"
			}
		}

		// Provider cache for DB-stored SSO providers (60-second TTL)
		var providerCache *auth.ProviderCache
		if enc != nil {
			providerCache = auth.NewProviderCache(db, enc, redirectURI, 60*time.Second)

			// Migrate legacy Entra env-var config to DB on first deploy
			if err := migrateLegacyEntraConfig(context.Background(), db, enc, tenantID, clientID, clientSecret, redirectURI); err != nil {
				log.Printf("Warning: legacy Entra migration failed: %v", err)
			}
		}

		var oidcProvider *auth.OIDCProvider
		if tenantID != "" && clientID != "" && clientSecret != "" && redirectURI != "" {
			var err error
			oidcProvider, err = auth.NewOIDCProvider(context.Background(), tenantID, clientID, clientSecret, redirectURI)
			if err != nil {
				log.Fatalf("Failed to initialize OIDC provider: %v", err)
			}
			log.Println("✓ OIDC provider ready (Entra)")
		} else {
			log.Println("ℹ No ENTRA_* env vars set — SSO login disabled; using local auth only")
		}

		authHandlers := auth.NewHandlers(oidcProvider, providerCache, sessions, db, os.Getenv("FRONTEND_URL"))

		// Local auth routes — always available
		router.HandleFunc("/auth/local/login", authHandlers.HandleLocalLogin).Methods("POST")
		router.Handle("/auth/local/change-password",
			auth.BearerOrCookie(sessions, db)(http.HandlerFunc(authHandlers.HandleChangePassword)),
		).Methods("POST")

		// SSO routes — available when env-var provider OR at least one DB-stored provider exists
		if oidcProvider != nil || providerCache != nil {
			router.HandleFunc("/auth/login", authHandlers.HandleLogin).Methods("GET")
			router.HandleFunc("/auth/callback", authHandlers.HandleCallback).Methods("GET")
		}

		router.HandleFunc("/auth/logout", authHandlers.HandleLogout).Methods("POST")

		// Auth-protected session route
		router.Handle("/auth/me",
			auth.BearerOrCookie(sessions, db)(http.HandlerFunc(authHandlers.HandleMe)),
		).Methods("GET")

		// User notification rules (per-user profile settings)
		profileRouter := router.PathPrefix("/api/v1/profile").Subrouter()
		profileRouter.Use(auth.BearerOrCookie(sessions, db))
		profileRouter.HandleFunc("/notification-rules", server.handleListNotificationRules).Methods("GET")
		profileRouter.HandleFunc("/notification-rules", server.handleCreateNotificationRule).Methods("POST")
		profileRouter.HandleFunc("/notification-rules/{id}", server.handleUpdateNotificationRule).Methods("PUT")
		profileRouter.HandleFunc("/notification-rules/{id}", server.handleDeleteNotificationRule).Methods("DELETE")

		// Superadmin-only routes
		superRouter := router.PathPrefix("/api/v1/admin").Subrouter()
		superRouter.Use(auth.BearerOrCookie(sessions, db))
		superRouter.Use(auth.RequireNoForceChange)
		superRouter.Use(auth.RequireSuperAdmin)

		// Full admin panel — only available when encryption key is configured
		if enc != nil {
			adminH := auth.NewAdminHandlers(db, enc, providerCache, lic)

			// Users
			superRouter.HandleFunc("/users", adminH.HandleListUsers).Methods("GET")
			superRouter.HandleFunc("/users", adminH.HandleCreateUser).Methods("POST")
			superRouter.HandleFunc("/users/{id}", adminH.HandleGetUser).Methods("GET")
			superRouter.HandleFunc("/users/{id}", adminH.HandleUpdateUser).Methods("PUT")
			superRouter.HandleFunc("/users/{id}", adminH.HandleDeleteUser).Methods("DELETE")
			superRouter.HandleFunc("/users/{id}/reset-password", adminH.HandleResetPassword).Methods("POST")

			// Groups
			superRouter.HandleFunc("/groups", adminH.HandleListGroups).Methods("GET")
			superRouter.HandleFunc("/groups", adminH.HandleCreateGroup).Methods("POST")
			superRouter.HandleFunc("/groups/{id}", adminH.HandleDeleteGroup).Methods("DELETE")
			superRouter.HandleFunc("/groups/{id}/members", adminH.HandleListGroupMembers).Methods("GET")
			superRouter.HandleFunc("/groups/{id}/members", adminH.HandleAddGroupMember).Methods("POST")
			superRouter.HandleFunc("/groups/{id}/members/{userId}", adminH.HandleRemoveGroupMember).Methods("DELETE")
			superRouter.HandleFunc("/groups/{id}/access", adminH.HandleListGroupAccess).Methods("GET")
			superRouter.HandleFunc("/groups/{id}/access", adminH.HandleGrantGroupAccess).Methods("POST")
			superRouter.HandleFunc("/groups/{id}/access/{teamId}", adminH.HandleRevokeGroupAccess).Methods("DELETE")

			// SSO providers
			superRouter.HandleFunc("/sso/providers", adminH.HandleListSSOProviders).Methods("GET")
			superRouter.HandleFunc("/sso/providers", adminH.HandleCreateSSOProvider).Methods("POST")
			superRouter.HandleFunc("/sso/providers/{id}", adminH.HandleGetSSOProvider).Methods("GET")
			superRouter.HandleFunc("/sso/providers/{id}", adminH.HandleUpdateSSOProvider).Methods("PUT")
			superRouter.HandleFunc("/sso/providers/{id}", adminH.HandleDeleteSSOProvider).Methods("DELETE")
			superRouter.HandleFunc("/sso/providers/{id}/test", adminH.HandleTestSSOProvider).Methods("POST")

			// Teams (read-only, for admin dropdowns)
			superRouter.HandleFunc("/teams", adminH.HandleListTeams).Methods("GET")
			superRouter.HandleFunc("/teams", adminH.HandleCreateTeam).Methods("POST")
			superRouter.HandleFunc("/teams/{id}", adminH.HandleDeleteTeam).Methods("DELETE")

			// Group mappings (AD group → team)
			superRouter.HandleFunc("/group-mappings", adminH.HandleListGroupMappings).Methods("GET")
			superRouter.HandleFunc("/group-mappings", adminH.HandleCreateGroupMapping).Methods("POST")
			superRouter.HandleFunc("/group-mappings/{id}", adminH.HandleDeleteGroupMapping).Methods("DELETE")

			// Password policy
			superRouter.HandleFunc("/password-policy", adminH.HandleGetPasswordPolicy).Methods("GET")
			superRouter.HandleFunc("/password-policy", adminH.HandleUpdatePasswordPolicy).Methods("PUT")

			// API tokens (personal access tokens)
			superRouter.HandleFunc("/tokens", adminH.HandleListTokens).Methods("GET")
			superRouter.HandleFunc("/tokens", adminH.HandleCreateToken).Methods("POST")
			superRouter.HandleFunc("/tokens/{id}", adminH.HandleDeleteToken).Methods("DELETE")

			// Platform-wide AI backend config (superadmin only)
			superRouter.HandleFunc("/system/ai", server.handleGetSystemAI).Methods("GET")
			superRouter.HandleFunc("/system/ai", server.handleUpsertSystemAI).Methods("PUT")
		}

		// Bootstrap admin on first run (if no local users exist)
		if err := bootstrapAdmin(context.Background(), db); err != nil {
			log.Printf("Warning: bootstrap admin check failed: %v", err)
		}

		// Bootstrap group mappings from environment (seeded by Helm configmap)
		if err := bootstrapGroupMappings(context.Background(), db); err != nil {
			log.Printf("Warning: group mapping bootstrap failed: %v", err)
		}
	} else {
		log.Println("⚠ AUTH_DISABLED=true — running without authentication (dev mode only)")
	}

	// --- Public routes (no auth required) ---
	router.HandleFunc("/api/v1/health", server.handleHealth).Methods("GET")
	// Webhook uses its own HMAC-based authentication
	router.HandleFunc("/api/v1/webhook/{teamId}/{secret}", server.handleWebhook).Methods("POST")

	// --- Protected API routes ---
	// Register on a subrouter so auth middleware is applied cleanly without path string matching
	apiRouter := router.PathPrefix("/api/v1/teams").Subrouter()
	if !authDisabled {
		apiRouter.Use(auth.BearerOrCookie(sessions, db))
		// Block users with force_password_change=true from calling any team API.
		// They must hit POST /auth/local/change-password first.
		apiRouter.Use(auth.RequireNoForceChange)
	}
	apiRouter.HandleFunc("/{teamId}/incidents", server.handleListIncidents).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/incidents/{incidentId}", server.handleGetIncident).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/incidents/{incidentId}/ack", server.handleAcknowledgeIncident).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/incidents/{incidentId}/resolve", server.handleResolveIncident).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/incidents/{incidentId}/reopen", server.handleReopenIncident).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/incidents/{incidentId}/snooze", server.handleSnoozeIncident).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/schedules", server.handleListSchedules).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/schedule", server.handleGetSchedule).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/schedule", server.handleUpsertSchedule).Methods("PUT")
	apiRouter.HandleFunc("/{teamId}/schedule/overrides", server.handleListOverrides).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/schedule/overrides", server.handleCreateOverride).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/schedule/overrides/{overrideId}", server.handleDeleteOverride).Methods("DELETE")
	apiRouter.HandleFunc("/{teamId}/oncall/now", server.handleGetCurrentOnCall).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/oncall/timeline", server.handleGetOnCallTimeline).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/members", server.handleListMembers).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/members/{userId}", server.handleUpdateMember).Methods("PUT")
	apiRouter.HandleFunc("/{teamId}/config", server.handleGetTeamConfig).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/config", server.handleUpsertTeamConfig).Methods("PUT")
	apiRouter.HandleFunc("/{teamId}/config/test-notification", server.handleTestNotification).Methods("POST")
	apiRouter.HandleFunc("/{teamId}/escalation", server.handleGetEscalationPolicy).Methods("GET")
	apiRouter.HandleFunc("/{teamId}/escalation", server.handleUpsertEscalationPolicy).Methods("PUT")

	// Wrap router with CORS middleware
	handler := corsMiddleware(router)

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	go func() {
		log.Printf("Wachd server listening on port %s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}

// corsMiddleware adds CORS headers to allow requests from the frontend.
// The allowed origin defaults to http://localhost:3000 and can be overridden
// by setting the CORS_ORIGIN environment variable.
func corsMiddleware(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"service": "wachd-server",
	})
}

// requireTeamAccess returns true if the session in the context grants access to teamID.
// When AUTH_DISABLED=true, all teams are accessible (dev mode).
func (s *Server) requireTeamAccess(r *http.Request, teamID uuid.UUID) bool {
	if os.Getenv("AUTH_DISABLED") == "true" {
		return true
	}
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		return false
	}
	return sess.HasTeamAccess(teamID)
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	secret := vars["secret"]

	// Rate-limit per source IP — 20 burst, 1/s sustained.
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	} else if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if !s.webhookLimiter.allow(ip) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// Parse team ID
	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}

	// Fetch team by ID then compare secret in constant time to avoid timing oracle.
	team, err := s.db.GetTeam(r.Context(), teamID)
	if err != nil || team == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(team.WebhookSecret), []byte(secret)) != 1 {
		log.Printf("handleWebhook: invalid secret for team %s from %s", teamIDStr, ip)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Enforce license monthly alert limit.
	alertCount, err := s.db.CountIncidentsThisMonth(r.Context())
	if err != nil {
		log.Printf("handleWebhook: count incidents: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if alertCount >= s.license.MaxAlertsMonth {
		log.Printf("handleWebhook: monthly alert limit reached (%d/%d) tier=%s",
			alertCount, s.license.MaxAlertsMonth, s.license.Tier)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":       "monthly alert limit reached",
			"limit":       s.license.MaxAlertsMonth,
			"tier":        string(s.license.Tier),
			"upgrade_url": "https://wachd.io/pricing",
		})
		return
	}

	// Read webhook payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// Parse webhook — auto-detects Grafana, Datadog, or generic format
	title, message, severity, source := parseWebhookPayload(body)

	// Create incident
	incident := &store.Incident{
		TeamID:       teamID,
		Title:        title,
		Message:      &message,
		Severity:     severity,
		Status:       "open",
		Source:       source,
		AlertPayload: body,
	}

	if err := s.db.CreateIncident(r.Context(), incident); err != nil {
		log.Printf("Failed to create incident: %v", err)
		http.Error(w, "Failed to create incident", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Created incident %s for team %s: %s", incident.ID, team.Name, incident.Title)

	// Enqueue job for worker to process
	if err := s.queue.EnqueueAlert(r.Context(), incident.ID, teamID, body); err != nil {
		log.Printf("Failed to enqueue job: %v", err)
		// Don't fail the request - incident is already saved
	}

	// Return 202 Accepted
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "accepted",
		"incident_id": incident.ID,
	})
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incidents, err := s.db.ListIncidents(r.Context(), teamID, 50, 0)
	if err != nil {
		log.Printf("Failed to list incidents: %v", err)
		http.Error(w, "Failed to list incidents", http.StatusInternalServerError)
		return
	}

	// Convert to API response format
	incidentResponses := make([]*store.IncidentResponse, len(incidents))
	for i, incident := range incidents {
		resp, err := incident.ToResponse()
		if err != nil {
			log.Printf("Failed to convert incident to response: %v", err)
			continue
		}
		incidentResponses[i] = resp
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"incidents": incidentResponses,
		"count":     len(incidentResponses),
	})
}

func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	incidentIDStr := vars["incidentId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}

	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incident, err := s.db.GetIncident(r.Context(), teamID, incidentID)
	if err != nil {
		log.Printf("Failed to get incident: %v", err)
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Convert to API response format with parsed JSONB fields
	resp, err := incident.ToResponse()
	if err != nil {
		log.Printf("Failed to convert incident to response: %v", err)
		http.Error(w, "Failed to format incident", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GET /api/v1/teams/{teamId}/schedules — list all schedules for a team
func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	schedules, err := s.cfg.ListSchedules(r.Context(), teamID)
	if err != nil {
		log.Printf("handleListSchedules: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type scheduleItem struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	items := make([]scheduleItem, len(schedules))
	for i, sc := range schedules {
		items[i] = scheduleItem{ID: sc.ID.String(), Name: sc.Name}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"schedules": items})
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	schedule, err := s.cfg.GetScheduleForAPI(r.Context(), teamID)
	if err != nil {
		log.Printf("Failed to get schedule: %v", err)
		http.Error(w, "Failed to load schedule", http.StatusInternalServerError)
		return
	}
	if schedule == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": false,
			"message":    "No on-call schedule configured for this team yet. Create one via PUT /schedule.",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schedule)
}

func (s *Server) handleGetCurrentOnCall(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	// Get current on-call member
	onCallMember, err := s.oncallManager.GetCurrentOnCall(r.Context(), teamID)
	if err != nil {
		log.Printf("Failed to get current on-call: %v", err)
		http.Error(w, "Failed to get on-call user", http.StatusInternalServerError)
		return
	}
	if onCallMember == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": false,
			"message":    "No on-call schedule configured for this team yet.",
		})
		return
	}

	now := time.Now()
	day := now.Weekday().String()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"configured": true,
		"user": map[string]interface{}{
			"id":     onCallMember.ID,
			"name":   onCallMember.Name,
			"email":  onCallMember.Email,
			"phone":  onCallMember.Phone,
			"source": onCallMember.Source,
		},
		"day": day,
	})
}

// handleGetOnCallTimeline returns per-day on-call coverage for a date range.
// GET /api/v1/teams/{teamId}/oncall/timeline?from=YYYY-MM-DD&days=N
func (s *Server) handleGetOnCallTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	// Parse query params.
	from := time.Now().UTC().Truncate(24 * time.Hour)
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse("2006-01-02", fromStr); err == nil {
			from = t.UTC()
		}
	}
	days := 14
	if daysStr := r.URL.Query().Get("days"); daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 && d <= 42 {
			days = d
		}
	}

	schedule, err := s.cfg.GetSchedule(ctx, teamID)
	if err != nil {
		log.Printf("timeline: get schedule: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
		return
	}
	if schedule == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"schedule_name": "", "layer_names": []string{},
			"from": from.Format("2006-01-02"), "days": days, "entries": []interface{}{},
		})
		return
	}

	// Member name lookup.
	teamMembers, err := s.cfg.GetTeamMembers(ctx, teamID)
	if err != nil {
		log.Printf("timeline: get members: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
		return
	}
	nameMap := make(map[uuid.UUID]string, len(teamMembers))
	for _, m := range teamMembers {
		nameMap[m.ID] = m.Name
	}

	// Overrides for the range.
	to := from.AddDate(0, 0, days)
	overrides, _ := s.cfg.ListOverridesForRange(ctx, schedule.ID, teamID, from, to)

	type tLayer struct {
		LayerName string `json:"layer_name"`
		UserID    string `json:"user_id,omitempty"`
		UserName  string `json:"user_name,omitempty"`
	}
	type tOverride struct {
		ID       string `json:"id"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		Reason   string `json:"reason,omitempty"`
		StartAt  string `json:"start_at"`
		EndAt    string `json:"end_at"`
	}
	type tEntry struct {
		Date          string     `json:"date"`
		Layers        []tLayer   `json:"layers"`
		Override      *tOverride `json:"override,omitempty"`
		FinalUserID   string     `json:"final_user_id,omitempty"`
		FinalUserName string     `json:"final_user_name,omitempty"`
	}

	var layerNames []string
	layerNamesSet := false
	entries := make([]tEntry, days)

	for i := 0; i < days; i++ {
		day := from.AddDate(0, 0, i)
		noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)

		layerResults, _ := oncall.ResolveAllLayersAt(schedule.RotationConfig, noon)

		if !layerNamesSet && len(layerResults) > 0 {
			layerNamesSet = true
			for _, lr := range layerResults {
				layerNames = append(layerNames, lr.LayerName)
			}
		}

		var layers []tLayer
		for _, lr := range layerResults {
			uid := ""
			name := ""
			if lr.UserID != uuid.Nil {
				uid = lr.UserID.String()
				name = nameMap[lr.UserID]
			}
			layers = append(layers, tLayer{LayerName: lr.LayerName, UserID: uid, UserName: name})
		}

		// Find override overlapping this day (any overlap, not just noon).
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		dayEnd := dayStart.Add(24 * time.Hour)
		var dayOverride *tOverride
		for j := range overrides {
			o := &overrides[j]
			if o.StartAt.Before(dayEnd) && o.EndAt.After(dayStart) {
				ov := &tOverride{
					ID: o.ID.String(), UserID: o.UserID.String(),
					UserName: nameMap[o.UserID],
					StartAt:  o.StartAt.Format(time.RFC3339),
					EndAt:    o.EndAt.Format(time.RFC3339),
				}
				if o.Reason != nil {
					ov.Reason = *o.Reason
				}
				dayOverride = ov
				break
			}
		}

		// Final: override user if set, else first layer with coverage.
		finalUID, finalName := "", ""
		if dayOverride != nil {
			finalUID, finalName = dayOverride.UserID, dayOverride.UserName
		} else {
			for _, l := range layers {
				if l.UserID != "" {
					finalUID, finalName = l.UserID, l.UserName
					break
				}
			}
		}

		entries[i] = tEntry{
			Date: day.Format("2006-01-02"), Layers: layers,
			Override: dayOverride, FinalUserID: finalUID, FinalUserName: finalName,
		}
	}

	if layerNames == nil {
		layerNames = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"schedule_name": schedule.Name,
		"layer_names":   layerNames,
		"from":          from.Format("2006-01-02"),
		"days":          days,
		"entries":       entries,
	})
}

func (s *Server) handleAcknowledgeIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	incidentIDStr := vars["incidentId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	// Verify incident belongs to team
	incident, err := s.db.GetIncident(r.Context(), teamID, incidentID)
	if err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Get current on-call member as acknowledger
	onCallMember, err := s.oncallManager.GetCurrentOnCall(r.Context(), teamID)
	if err != nil {
		log.Printf("Failed to get on-call member: %v", err)
		http.Error(w, "Failed to get on-call user", http.StatusInternalServerError)
		return
	}

	// Use Nil UUID if no one is on-call (still record the ack)
	acknowledgerID := uuid.Nil
	acknowledgerName := "unknown"
	if onCallMember != nil {
		acknowledgerID = onCallMember.ID
		acknowledgerName = onCallMember.Name
	}

	// Acknowledge the incident
	if err := s.db.AcknowledgeIncident(r.Context(), teamID, incidentID, acknowledgerID); err != nil {
		log.Printf("Failed to acknowledge incident: %v", err)
		http.Error(w, "Failed to acknowledge incident", http.StatusInternalServerError)
		return
	}

	// Cancel any pending delayed notifications for this incident (e.g. queued voice calls)
	if err := s.db.CancelPendingNotificationsForIncident(r.Context(), incidentID); err != nil {
		log.Printf("warn: cancel pending notifications for incident %s: %v", incidentID, err)
		// non-fatal — ack succeeded
	}

	log.Printf("✓ Incident %s acknowledged by %s", incident.Title, acknowledgerName)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "acknowledged",
	})
}

func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	incidentIDStr := vars["incidentId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	// Verify incident belongs to team
	incident, err := s.db.GetIncident(r.Context(), teamID, incidentID)
	if err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Update incident status to resolved
	now := time.Now()
	query := `
		UPDATE incidents
		SET status = 'resolved', resolved_at = $1, updated_at = $2
		WHERE id = $3 AND team_id = $4
	`
	_, err = s.db.Pool().Exec(r.Context(), query, now, now, incidentID, teamID)
	if err != nil {
		log.Printf("Failed to resolve incident: %v", err)
		http.Error(w, "Failed to resolve incident", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Incident %s resolved", incident.Title)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "resolved",
		"resolved_at": now,
	})
}

func (s *Server) handleReopenIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	incidentIDStr := vars["incidentId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	// Verify incident belongs to team
	incident, err := s.db.GetIncident(r.Context(), teamID, incidentID)
	if err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Update incident status to open, clear timestamps
	now := time.Now()
	query := `
		UPDATE incidents
		SET status = 'open',
		    acknowledged_at = NULL,
		    resolved_at = NULL,
		    assigned_to = NULL,
		    updated_at = $1
		WHERE id = $2 AND team_id = $3
	`
	_, err = s.db.Pool().Exec(r.Context(), query, now, incidentID, teamID)
	if err != nil {
		log.Printf("Failed to reopen incident: %v", err)
		http.Error(w, "Failed to reopen incident", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Incident %s reopened", incident.Title)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "open",
	})
}

func (s *Server) handleSnoozeIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamIDStr := vars["teamId"]
	incidentIDStr := vars["incidentId"]

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		http.Error(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	// Parse request body
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Minutes <= 0 || req.Minutes > 10080 { // max 7 days
		http.Error(w, "Minutes must be between 1 and 10080", http.StatusBadRequest)
		return
	}

	// Verify incident belongs to team
	incident, err := s.db.GetIncident(r.Context(), teamID, incidentID)
	if err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Calculate snooze until time
	snoozeUntil := time.Now().Add(time.Duration(req.Minutes) * time.Minute)

	// Update incident with snooze time and status
	query := `
		UPDATE incidents
		SET status = 'snoozed', snoozed_until = $1, updated_at = $2
		WHERE id = $3 AND team_id = $4
	`
	_, err = s.db.Pool().Exec(r.Context(), query, snoozeUntil, time.Now(), incidentID, teamID)
	if err != nil {
		log.Printf("Failed to snooze incident: %v", err)
		http.Error(w, "Failed to snooze incident", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Incident %s snoozed for %d minutes until %s", incident.Title, req.Minutes, snoozeUntil.Format("15:04"))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "snoozed",
		"snoozed_until": snoozeUntil,
	})
}

// teamConfigPublic is the API-safe view of TeamConfig.
// It never exposes encrypted token values — only whether they are set.
type teamConfigPublic struct {
	TeamID             string   `json:"team_id"`
	WebhookSecret      string   `json:"webhook_secret"`
	SlackWebhookURL    *string  `json:"slack_webhook_url,omitempty"`
	SlackChannel       *string  `json:"slack_channel,omitempty"`
	GitHubTokenSet     bool     `json:"github_token_set"`
	GitHubRepos        []string `json:"github_repos,omitempty"`
	PrometheusEndpoint *string  `json:"prometheus_endpoint,omitempty"`
	LokiEndpoint       *string  `json:"loki_endpoint,omitempty"`
}

// teamConfigInput is the request body for PUT /{teamId}/config.
type teamConfigInput struct {
	SlackWebhookURL    *string  `json:"slack_webhook_url"`
	SlackChannel       *string  `json:"slack_channel"`
	GitHubToken        string   `json:"github_token"`  // plaintext; encrypted before storing
	GitHubRepos        []string `json:"github_repos"`
	PrometheusEndpoint *string  `json:"prometheus_endpoint"`
	LokiEndpoint       *string  `json:"loki_endpoint"`
}

func (s *Server) handleGetTeamConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	team, err := s.cfg.GetTeam(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load team", http.StatusInternalServerError)
		return
	}

	cfg, err := s.cfg.GetTeamConfig(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	pub := teamConfigPublic{TeamID: teamID.String()}
	if team != nil {
		pub.WebhookSecret = team.WebhookSecret
	}
	if cfg != nil {
		pub.SlackWebhookURL = cfg.SlackWebhookURL
		pub.SlackChannel = cfg.SlackChannel
		pub.GitHubTokenSet = cfg.GitHubTokenEncrypted != nil && *cfg.GitHubTokenEncrypted != ""
		pub.PrometheusEndpoint = cfg.PrometheusEndpoint
		pub.LokiEndpoint = cfg.LokiEndpoint
		if cfg.GitHubRepos != nil {
			_ = json.Unmarshal(cfg.GitHubRepos, &pub.GitHubRepos)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pub)
}

func (s *Server) handleUpsertTeamConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	var input teamConfigInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate URLs to prevent SSRF — only public http/https endpoints allowed.
	for _, u := range []*string{input.PrometheusEndpoint, input.LokiEndpoint, input.SlackWebhookURL} {
		if u != nil && *u != "" {
			if err := validate.EndpointURL(*u); err != nil {
				http.Error(w, "invalid endpoint URL: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	// Validate field lengths.
	if input.SlackWebhookURL != nil && len(*input.SlackWebhookURL) > 2000 {
		http.Error(w, "slack_webhook_url too long", http.StatusBadRequest)
		return
	}
	if input.PrometheusEndpoint != nil && len(*input.PrometheusEndpoint) > 2000 {
		http.Error(w, "prometheus_endpoint too long", http.StatusBadRequest)
		return
	}
	if input.LokiEndpoint != nil && len(*input.LokiEndpoint) > 2000 {
		http.Error(w, "loki_endpoint too long", http.StatusBadRequest)
		return
	}
	if len(input.GitHubRepos) > 50 {
		http.Error(w, "too many github_repos (max 50)", http.StatusBadRequest)
		return
	}

	// Load existing config so we preserve fields the caller didn't send
	existing, err := s.cfg.GetTeamConfig(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load existing config", http.StatusInternalServerError)
		return
	}

	tc := &store.TeamConfig{TeamID: teamID}
	if existing != nil {
		*tc = *existing
	}

	// Apply updates
	if input.SlackWebhookURL != nil {
		tc.SlackWebhookURL = input.SlackWebhookURL
	}
	if input.SlackChannel != nil {
		tc.SlackChannel = input.SlackChannel
	}
	if input.PrometheusEndpoint != nil {
		tc.PrometheusEndpoint = input.PrometheusEndpoint
	}
	if input.LokiEndpoint != nil {
		tc.LokiEndpoint = input.LokiEndpoint
	}

	// Encrypt GitHub token if provided
	if input.GitHubToken != "" {
		if s.enc == nil {
			http.Error(w, "encryption not configured — WACHD_ENCRYPTION_KEY required to store tokens", http.StatusServiceUnavailable)
			return
		}
		encrypted, err := s.enc.Encrypt(input.GitHubToken)
		if err != nil {
			http.Error(w, "failed to encrypt token", http.StatusInternalServerError)
			return
		}
		tc.GitHubTokenEncrypted = &encrypted
	}

	// Encode repos list
	if input.GitHubRepos != nil {
		reposJSON, err := json.Marshal(input.GitHubRepos)
		if err != nil {
			http.Error(w, "invalid github_repos", http.StatusBadRequest)
			return
		}
		tc.GitHubRepos = reposJSON
	}

	if err := s.cfg.UpsertTeamConfig(r.Context(), tc); err != nil {
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}

	// Return the same safe public view
	pub := teamConfigPublic{
		TeamID:             teamID.String(),
		SlackWebhookURL:    tc.SlackWebhookURL,
		SlackChannel:       tc.SlackChannel,
		GitHubTokenSet:     tc.GitHubTokenEncrypted != nil && *tc.GitHubTokenEncrypted != "",
		PrometheusEndpoint: tc.PrometheusEndpoint,
		LokiEndpoint:       tc.LokiEndpoint,
	}
	if tc.GitHubRepos != nil {
		_ = json.Unmarshal(tc.GitHubRepos, &pub.GitHubRepos)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pub)
}

// handleTestNotification sends a test message on the requested channel (slack or email)
// using the team's current saved configuration. Returns 200 on success, 400/500 on error.
func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid teamId", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}

	var req struct {
		Channel string `json:"channel"` // "slack" or "email"
		Email   string `json:"email"`   // recipient for email test (optional — falls back to session email)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	cfg, err := s.cfg.GetTeamConfig(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load team config", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	switch req.Channel {
	case "slack":
		if cfg == nil || cfg.SlackWebhookURL == nil || *cfg.SlackWebhookURL == "" {
			http.Error(w, `{"error":"Slack webhook URL not configured"}`, http.StatusBadRequest)
			return
		}
		channel := ""
		if cfg.SlackChannel != nil {
			channel = *cfg.SlackChannel
		}
		n := notify.NewSlackNotifier(*cfg.SlackWebhookURL, channel)
		if err := n.SendTestMessage(ctx); err != nil {
			log.Printf("handleTestNotification: slack send failed: %v", err)
			http.Error(w, "Slack notification failed — check webhook URL and channel config", http.StatusBadGateway)
			return
		}

	case "email":
		smtpHost := os.Getenv("SMTP_HOST")
		if smtpHost == "" {
			http.Error(w, "SMTP not configured on this server", http.StatusBadRequest)
			return
		}
		to := req.Email
		if to == "" {
			sess := auth.SessionFromContext(ctx)
			if sess != nil {
				to = sess.Email
			}
		}
		if to == "" {
			http.Error(w, "email recipient required", http.StatusBadRequest)
			return
		}
		n := notify.NewEmailNotifier(
			smtpHost,
			os.Getenv("SMTP_PORT"),
			os.Getenv("SMTP_FROM"),
			os.Getenv("SMTP_USERNAME"),
			os.Getenv("SMTP_PASSWORD"),
		)
		if err := n.SendTestMessage(ctx, to); err != nil {
			log.Printf("handleTestNotification: email send failed: %v", err)
			http.Error(w, "Email delivery failed — check SMTP config and recipient address", http.StatusBadGateway)
			return
		}

	default:
		http.Error(w, "channel must be 'slack' or 'email'", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

// handleGetSystemAI returns the platform-wide AI backend configuration.
// Only superadmins can access this endpoint (enforced by superRouter middleware).
func (s *Server) handleGetSystemAI(w http.ResponseWriter, r *http.Request) {
	sc, err := s.db.GetSystemConfig(r.Context())
	if err != nil {
		http.Error(w, "failed to load system config", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sc)
}

// handleUpsertSystemAI updates the platform-wide AI backend configuration.
// Only superadmins can access this endpoint (enforced by superRouter middleware).
func (s *Server) handleUpsertSystemAI(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil || sess.LocalUserID == nil {
		writeForbidden(w)
		return
	}

	var input struct {
		AIBackend string  `json:"ai_backend"`
		AIModel   *string `json:"ai_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	allowed := map[string]bool{"ollama": true, "claude": true, "openai": true, "gemini": true}
	if input.AIBackend == "" || !allowed[input.AIBackend] {
		http.Error(w, "ai_backend is required: must be ollama, claude, openai, or gemini", http.StatusBadRequest)
		return
	}

	sc, err := s.db.UpsertSystemConfig(r.Context(), input.AIBackend, input.AIModel, *sess.LocalUserID)
	if err != nil {
		http.Error(w, "failed to save system config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sc)
}

func (s *Server) handleGetEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	policy, err := s.cfg.GetEscalationPolicy(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load escalation policy", http.StatusInternalServerError)
		return
	}

	type response struct {
		Config    json.RawMessage `json:"config"`
		UpdatedAt *time.Time      `json:"updated_at,omitempty"`
	}
	resp := response{}
	if policy != nil {
		resp.Config = json.RawMessage(policy.Config)
		resp.UpdatedAt = &policy.UpdatedAt
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleUpsertEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}

	var input struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(input.Config) == 0 {
		http.Error(w, "config is required", http.StatusBadRequest)
		return
	}

	// Validate it's a JSON object
	var check map[string]interface{}
	if err := json.Unmarshal(input.Config, &check); err != nil {
		http.Error(w, "config must be a JSON object", http.StatusBadRequest)
		return
	}

	policy := &store.EscalationPolicy{
		TeamID: teamID,
		Config: []byte(input.Config),
	}
	if err := s.cfg.UpsertEscalationPolicy(r.Context(), policy); err != nil {
		http.Error(w, "failed to save escalation policy", http.StatusInternalServerError)
		return
	}

	type response struct {
		Config    json.RawMessage `json:"config"`
		UpdatedAt time.Time       `json:"updated_at"`
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		Config:    input.Config,
		UpdatedAt: policy.UpdatedAt,
	})
}

// requireTeamAdmin returns true when the caller holds admin or superadmin on teamID.
func (s *Server) requireTeamAdmin(r *http.Request, teamID uuid.UUID) bool {
	if os.Getenv("AUTH_DISABLED") == "true" {
		return true
	}
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		return false
	}
	if sess.IsSuperAdmin {
		return true
	}
	return sess.Roles[teamID.String()] == "admin"
}

// callerID extracts the current user's UUID from the session for audit fields.
func (s *Server) callerID(r *http.Request) uuid.UUID {
	if os.Getenv("AUTH_DISABLED") == "true" {
		return uuid.Nil
	}
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		return uuid.Nil
	}
	if sess.LocalUserID != nil {
		return *sess.LocalUserID
	}
	if sess.IdentityID != nil {
		return *sess.IdentityID
	}
	return uuid.Nil
}

// ── Member (on-call roster) handlers ─────────────────────────────────────────

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}
	members, err := s.cfg.GetTeamMembers(r.Context(), teamID)
	if err != nil {
		log.Printf("handleListMembers: %v", err)
		http.Error(w, "failed to list members", http.StatusInternalServerError)
		return
	}
	if members == nil {
		members = []*store.TeamMember{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": members, "count": len(members)})
}

// handleUpdateMember allows a team admin to set/clear a member's phone number.
// Identity creation and team access are managed via the Admin panel (groups).
func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	userID, err := uuid.Parse(vars["userId"])
	if err != nil {
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}
	var input struct {
		Phone  *string `json:"phone"`
		Source string  `json:"source"` // "local" | "sso"
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.Source != "local" && input.Source != "sso" {
		http.Error(w, "source must be local or sso", http.StatusBadRequest)
		return
	}
	if err := s.cfg.UpdateMemberPhone(r.Context(), userID, input.Source, input.Phone); err != nil {
		log.Printf("handleUpdateMember: %v", err)
		http.Error(w, "failed to update phone", http.StatusInternalServerError)
		return
	}
	member, err := s.cfg.GetMemberByID(r.Context(), userID)
	if err != nil || member == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(member)
}

// ── Schedule management handlers ─────────────────────────────────────────────

func (s *Server) handleUpsertSchedule(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}
	var input struct {
		Name           string                 `json:"name"`
		RotationConfig map[string]interface{} `json:"rotation_config"`
		Enabled        *bool                  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		input.Name = "Primary On-Call"
	}
	if input.RotationConfig == nil {
		http.Error(w, "rotation_config is required", http.StatusBadRequest)
		return
	}
	rotationJSON, err := json.Marshal(input.RotationConfig)
	if err != nil {
		http.Error(w, "invalid rotation_config", http.StatusBadRequest)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sched := &store.Schedule{
		TeamID:         teamID,
		Name:           input.Name,
		RotationConfig: rotationJSON,
		Enabled:        enabled,
	}
	if err := s.cfg.UpsertSchedule(r.Context(), sched); err != nil {
		log.Printf("handleUpsertSchedule: %v", err)
		http.Error(w, "failed to save schedule", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	resp, err := s.cfg.GetScheduleForAPI(r.Context(), teamID)
	if err != nil || resp == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sched)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ── Override handlers ─────────────────────────────────────────────────────────

func (s *Server) handleListOverrides(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}
	sched, err := s.cfg.GetSchedule(r.Context(), teamID)
	if err != nil {
		log.Printf("handleListOverrides: get schedule: %v", err)
		http.Error(w, "failed to get schedule", http.StatusInternalServerError)
		return
	}
	if sched == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"overrides": []interface{}{}, "count": 0})
		return
	}
	overrides, err := s.cfg.ListOverridesForSchedule(r.Context(), sched.ID, teamID)
	if err != nil {
		log.Printf("handleListOverrides: %v", err)
		http.Error(w, "failed to list overrides", http.StatusInternalServerError)
		return
	}
	if overrides == nil {
		overrides = []store.ScheduleOverride{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"overrides": overrides, "count": len(overrides)})
}

func (s *Server) handleCreateOverride(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	// responders and admins can create overrides for themselves/others
	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}
	var input struct {
		UserID  string  `json:"user_id"`
		StartAt string  `json:"start_at"`
		EndAt   string  `json:"end_at"`
		Reason  *string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	userID, err := uuid.Parse(input.UserID)
	if err != nil {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}
	startAt, err := time.Parse(time.RFC3339, input.StartAt)
	if err != nil {
		http.Error(w, "invalid start_at (use RFC3339 format)", http.StatusBadRequest)
		return
	}
	endAt, err := time.Parse(time.RFC3339, input.EndAt)
	if err != nil {
		http.Error(w, "invalid end_at (use RFC3339 format)", http.StatusBadRequest)
		return
	}
	if !endAt.After(startAt) {
		http.Error(w, "end_at must be after start_at", http.StatusBadRequest)
		return
	}
	sched, err := s.cfg.GetSchedule(r.Context(), teamID)
	if err != nil {
		log.Printf("handleCreateOverride: get schedule: %v", err)
		http.Error(w, "failed to get schedule", http.StatusInternalServerError)
		return
	}
	if sched == nil {
		http.Error(w, "no schedule configured for this team", http.StatusConflict)
		return
	}
	o := &store.ScheduleOverride{
		ScheduleID: sched.ID,
		TeamID:     teamID,
		UserID:     userID,
		StartAt:    startAt,
		EndAt:      endAt,
		Reason:     input.Reason,
		CreatedBy:  s.callerID(r),
	}
	if err := s.cfg.CreateOverride(r.Context(), o); err != nil {
		log.Printf("handleCreateOverride: %v", err)
		http.Error(w, "failed to create override", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(o)
}

func (s *Server) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}
	overrideID, err := uuid.Parse(vars["overrideId"])
	if err != nil {
		http.Error(w, "invalid override ID", http.StatusBadRequest)
		return
	}
	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}
	if err := s.cfg.DeleteOverride(r.Context(), overrideID, teamID); err != nil {
		log.Printf("handleDeleteOverride: %v", err)
		http.Error(w, "failed to delete override", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// migrateLegacyEntraConfig inserts a DB row for the Entra SSO config from env vars,// but only on the FIRST deploy (when sso_providers is empty and all env vars are set).
// After migration the env vars become advisory-only; the DB record is authoritative.
func migrateLegacyEntraConfig(ctx context.Context, db *store.DB, enc *auth.Encryptor, tenantID, clientID, clientSecret, redirectURI string) error {
	if tenantID == "" || clientID == "" || clientSecret == "" {
		return nil // no legacy config to migrate
	}

	existing, err := db.ListSSOProviders(ctx, false)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil // already migrated
	}

	encSecret, err := enc.Encrypt(clientSecret)
	if err != nil {
		return fmt.Errorf("encrypt legacy secret: %w", err)
	}

	issuer := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)
	_, err = db.CreateSSOProvider(ctx, store.SSOProviderInput{
		Name:            "Microsoft Entra (migrated)",
		ProviderType:    "oidc",
		IssuerURL:       issuer,
		ClientID:        clientID,
		ClientSecretEnc: encSecret,
		Scopes:          []string{"openid", "profile", "email", "offline_access"},
		Enabled:         true,
		AutoProvision:   true,
	})
	if err != nil {
		return fmt.Errorf("insert legacy entra config: %w", err)
	}
	log.Println("✓ Migrated legacy ENTRA_* env vars to sso_providers table")
	return nil
}

// bootstrapAdmin creates the initial superadmin local user on first run.
// It only runs when the local_users table is empty and prints the generated
// password to stdout ONCE — it is never stored in plaintext again.
func bootstrapAdmin(ctx context.Context, db *store.DB) error {
	count, err := db.CountLocalUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // already bootstrapped
	}

	password, err := generateAdminPassword()
	if err != nil {
		return fmt.Errorf("generate admin password: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	_, err = db.CreateLocalUser(ctx,
		"wachd_admin",
		"admin@wachd.local",
		"Wachd Admin",
		hash,
		true,  // isSuperAdmin
		true,  // forcePasswordChange — must change on first login
	)
	if err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	log.Println("╔════════════════════════════════════════════════╗")
	log.Println("║      WACHD — BOOTSTRAP ADMIN CREATED          ║")
	log.Println("╠════════════════════════════════════════════════╣")
	log.Printf( "║  Username: %-35s║", "wachd_admin")
	log.Printf( "║  Password: %-35s║", password)
	log.Println("╠════════════════════════════════════════════════╣")
	log.Println("║  ⚠  Change this password immediately!         ║")
	log.Println("║  POST /auth/local/login  (then /change-password)║")
	log.Println("╚════════════════════════════════════════════════╝")

	return nil
}

// generateAdminPassword returns a random 16-character password that satisfies
// the default policy (upper, lower, digit, special).
func generateAdminPassword() (string, error) {
	const upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	const lower   = "abcdefghjkmnpqrstuvwxyz"
	const digits  = "23456789"
	const special = "!@#$%^&*"
	const all     = upper + lower + digits + special

	// Guarantee at least one of each required class
	pick := func(charset string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return 0, err
		}
		return charset[n.Int64()], nil
	}

	buf := make([]byte, 16)
	for i, charset := range []string{upper, lower, digits, special} {
		b, err := pick(charset)
		if err != nil {
			return "", err
		}
		buf[i] = b
	}
	for i := 4; i < 16; i++ {
		b, err := pick(all)
		if err != nil {
			return "", err
		}
		buf[i] = b
	}

	// Fisher-Yates shuffle to remove positional bias
	for i := len(buf) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		j := jBig.Int64()
		buf[i], buf[j] = buf[j], buf[i]
	}

	return string(buf), nil
}

// bootstrapGroupMappings seeds group mappings from numbered env vars written by the Helm configmap.
// It reads GROUP_MAPPING_0_PROVIDER, GROUP_MAPPING_0_GROUP_ID, GROUP_MAPPING_0_GROUP_NAME,
// GROUP_MAPPING_0_TEAM_NAME, GROUP_MAPPING_0_ROLE, then _1_, _2_, etc. until a gap is found.
func bootstrapGroupMappings(ctx context.Context, db *store.DB) error {
	for i := 0; ; i++ {
		prefix := fmt.Sprintf("GROUP_MAPPING_%d_", i)
		groupID := os.Getenv(prefix + "GROUP_ID")
		if groupID == "" {
			break // no more mappings
		}

		provider := os.Getenv(prefix + "PROVIDER")
		if provider == "" {
			provider = "entra"
		}
		groupName := os.Getenv(prefix + "GROUP_NAME")
		teamName := os.Getenv(prefix + "TEAM_NAME")
		role := os.Getenv(prefix + "ROLE")
		if role == "" {
			role = "viewer"
		}

		if teamName == "" {
			log.Printf("Warning: GROUP_MAPPING_%d missing TEAM_NAME, skipping", i)
			continue
		}

		// Ensure team exists (create if not)
		team, err := db.GetOrCreateTeamByName(ctx, teamName)
		if err != nil {
			return fmt.Errorf("bootstrap mapping %d: get or create team %q: %w", i, teamName, err)
		}

		var gn *string
		if groupName != "" {
			gn = &groupName
		}
		if err := db.EnsureGroupMappingBootstrap(ctx, provider, groupID, gn, team.ID, role); err != nil {
			return fmt.Errorf("bootstrap mapping %d: %w", i, err)
		}
		log.Printf("✓ Group mapping: %s group %s → team %q (%s)", provider, groupID, teamName, role)
	}
	return nil
}

// bootstrapFirstTeam creates a default team on first run when the database is empty.
func bootstrapFirstTeam(db *store.DB) error {
	ctx := context.Background()
	count, err := db.CountTeams(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	secret, err := generateSecret()
	if err != nil {
		return fmt.Errorf("failed to generate webhook secret: %w", err)
	}

	team, err := db.CreateTeam(ctx, "Default Team", secret)
	if err != nil {
		return fmt.Errorf("failed to create default team: %w", err)
	}

	log.Println("╔══════════════════════════════════════════════════════╗")
	log.Println("║              WACHD — FIRST RUN SETUP                ║")
	log.Println("╠══════════════════════════════════════════════════════╣")
	log.Printf( "║  Team ID:       %-36s  ║", team.ID)
	log.Printf( "║  Webhook secret: %-35s  ║", secret)
	log.Println("╠══════════════════════════════════════════════════════╣")
	log.Println("║  Send alerts to:                                     ║")
	log.Printf( "║  POST /api/v1/webhook/%s/  ║", team.ID)
	log.Printf( "║  Header or path secret: %-28s  ║", secret)
	log.Println("╚══════════════════════════════════════════════════════╝")

	return nil
}

// generateSecret returns a 32-byte cryptographically random hex string.
func generateSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ── User notification rules ──────────────────────────────────────────────────

func (s *Server) handleListNotificationRules(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, userSource := sessionIdentity(sess)

	rules, err := s.db.ListUserNotificationRules(r.Context(), userID, userSource)
	if err != nil {
		log.Printf("list notification rules: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if rules == nil {
		rules = []*store.UserNotificationRule{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": rules})
}

func (s *Server) handleCreateNotificationRule(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, userSource := sessionIdentity(sess)

	var req struct {
		EventType    string `json:"event_type"`
		Channel      string `json:"channel"`
		DelayMinutes int    `json:"delay_minutes"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	validEvents := map[string]bool{"new_alert": true, "ack": true, "resolve": true}
	validChannels := map[string]bool{"email": true, "sms": true, "voice": true, "slack": true}
	if !validEvents[req.EventType] {
		http.Error(w, "event_type must be one of: new_alert, ack, resolve", http.StatusBadRequest)
		return
	}
	if !validChannels[req.Channel] {
		http.Error(w, "channel must be one of: email, sms, voice, slack", http.StatusBadRequest)
		return
	}
	if req.DelayMinutes < 0 || req.DelayMinutes > 1440 {
		http.Error(w, "delay_minutes must be between 0 and 1440", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule := &store.UserNotificationRule{
		UserID:       userID,
		UserSource:   userSource,
		EventType:    req.EventType,
		Channel:      req.Channel,
		DelayMinutes: req.DelayMinutes,
		Enabled:      enabled,
	}
	created, err := s.db.UpsertUserNotificationRule(r.Context(), rule)
	if err != nil {
		log.Printf("create notification rule: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": created})
}

func (s *Server) handleUpdateNotificationRule(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, userSource := sessionIdentity(sess)

	ruleID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Enabled      *bool `json:"enabled"`
		DelayMinutes *int  `json:"delay_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Enabled == nil && req.DelayMinutes == nil {
		http.Error(w, "provide enabled or delay_minutes", http.StatusBadRequest)
		return
	}
	if req.DelayMinutes != nil && (*req.DelayMinutes < 0 || *req.DelayMinutes > 1440) {
		http.Error(w, "delay_minutes must be between 0 and 1440", http.StatusBadRequest)
		return
	}

	// Load current values so we can apply partial updates
	rules, err := s.db.ListUserNotificationRules(r.Context(), userID, userSource)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var current *store.UserNotificationRule
	for _, ru := range rules {
		if ru.ID == ruleID {
			current = ru
			break
		}
	}
	if current == nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	enabled := current.Enabled
	delay := current.DelayMinutes
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.DelayMinutes != nil {
		delay = *req.DelayMinutes
	}

	updated, err := s.db.UpdateUserNotificationRule(r.Context(), ruleID, userID, userSource, enabled, delay)
	if err != nil {
		log.Printf("update notification rule: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": updated})
}

func (s *Server) handleDeleteNotificationRule(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, userSource := sessionIdentity(sess)

	ruleID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteUserNotificationRule(r.Context(), ruleID, userID, userSource); err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sessionIdentity returns the identity UUID and source ("local" | "sso") for the
// logged-in user. Local users use LocalUserID; SSO users use IdentityID.
func sessionIdentity(sess *auth.Session) (uuid.UUID, string) {
	if sess.LocalUserID != nil {
		return *sess.LocalUserID, "local"
	}
	if sess.IdentityID != nil {
		return *sess.IdentityID, "sso"
	}
	return uuid.Nil, "local"
}
