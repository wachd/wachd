// Core types for the Wachd dashboard

export interface Team {
  id: string;
  name: string;
  created_at: string;
}

export interface User {
  id: string;
  team_id: string;
  name: string;
  email: string;
  phone?: string;
  role: 'viewer' | 'responder' | 'admin';
  created_at: string;
  updated_at: string;
}

export interface Incident {
  id: string;
  team_id: string;
  title: string;
  message?: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
  status: 'open' | 'acknowledged' | 'resolved' | 'snoozed';
  source: string;
  fired_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  snoozed_until?: string;
  alert_payload: Record<string, unknown>;
  context?: IncidentContext;
  analysis?: IncidentAnalysis;
  created_at: string;
  updated_at: string;
  assigned_to?: string;
}

export interface IncidentContext {
  commits: Commit[];
  logs: LogLine[];
  metrics: MetricPoint[];
}

export interface Commit {
  sha: string;
  message: string;
  author: string;
  timestamp: string;
}

export interface LogLine {
  timestamp: string;
  message: string;
  level: string;
  labels: Record<string, string>;
}

export interface MetricPoint {
  timestamp: string;
  value: number;
}

export type EvidenceClassification =
  | 'CONFIRMED_ROOT_CAUSE'
  | 'PROBABLE_ROOT_CAUSE'
  | 'TEMPORAL_CORRELATION_ONLY'
  | 'INSUFFICIENT_EVIDENCE';

// A reference the AI cited. Wachd sets `verified` after checking it against the
// data it actually collected.
export interface EvidenceRef {
  type: string; // commit | metric | log
  ref: string;
  detail?: string;
  verified?: boolean;
}

// Every field after `correlations` is absent on incidents analysed before
// evidence verification (v0.4.37), so all of them are optional.
export interface IncidentAnalysis {
  root_cause?: string;
  suggested_action?: string;
  confidence?: 'high' | 'medium' | 'low';
  key_findings?: string[];
  correlations?: string[];
  evidence_classification?: EvidenceClassification | string;
  is_deployment_cause?: boolean;
  causal_commit?: string;
  evidence?: EvidenceRef[];
  rejected_evidence?: string[];
  warnings?: string[];
}

export interface Schedule {
  id: string;
  team_id: string;
  name: string;
  rotation_config: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

// TeamMember is a unified identity sourced from local_users or sso_identities.
// This is what gets returned from GET /teams/:id/members and used in schedules.
export interface TeamMember {
  id: string;
  source: 'local' | 'sso';
  team_id: string;
  name: string;
  email: string;
  phone?: string;
  role: 'viewer' | 'responder' | 'admin';
}

// OnCallUser is returned by GET /teams/:id/oncall/now
export interface OnCallUser {
  user: TeamMember;
  day: string;
}

export interface ScheduleOverride {
  id: string;
  schedule_id: string;
  team_id: string;
  start_at: string;
  end_at: string;
  user_id: string;
  reason?: string;
  created_by: string;
  created_at: string;
}



export interface SimilarIncident {
  incident_id: string;
  title: string;
  score: number;
  reason: string;
  occurred_at?: string;
  resolution?: string;
}

export interface GraphConfig {
  enabled: boolean;
  min_similarity_score: number;
}

export interface GraphNode {
  id: string;
  team_id: string;
  type: string;
  status: 'pending' | 'permanent';
  label: string;
  external_id?: string;
  properties?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface GraphEdge {
  id: string;
  team_id: string;
  from_node_id: string;
  to_node_id: string;
  type: string;
  status: 'pending' | 'permanent';
  weight: number;
  properties?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface IncidentGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface TimelineEvent {
  time: string;
  kind:
    | 'alert_fired'
    | 'commit'
    | 'log_spike'
    | 'analysis_complete'
    | 'notification_sent'
    | 'acknowledged'
    | 'resolved';
  title: string;
  detail?: string;
  meta?: Record<string, string>;
}


export interface GraphService {
  id: string;
  name: string;
  label: string;
  description?: string;
  created_at: string;
  updated_at: string;
}

export interface GraphServiceInput {
  name: string;
  label?: string;
  description?: string;
}
