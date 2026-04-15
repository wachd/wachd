{{/*
Expand the name of the chart.
*/}}
{{- define "wachd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "wachd.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "wachd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "wachd.labels" -}}
helm.sh/chart: {{ include "wachd.chart" . }}
{{ include "wachd.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "wachd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "wachd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "wachd.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "wachd.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the proper image name
*/}}
{{- define "wachd.image" -}}
{{- $registry := .Values.image.registry -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- end }}

{{/*
Return the PostgreSQL connection string
*/}}
{{- define "wachd.databaseURL" -}}
{{- if .Values.postgres.enabled -}}
{{- printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable" .Values.postgres.auth.username .Values.postgres.auth.password (include "wachd.fullname" .) .Values.postgres.auth.database -}}
{{- else -}}
{{- printf "postgres://%s:$(POSTGRES_PASSWORD)@%s:%d/%s?sslmode=%s" .Values.postgres.external.username .Values.postgres.external.host (int .Values.postgres.external.port) .Values.postgres.external.database .Values.postgres.external.sslMode -}}
{{- end -}}
{{- end }}

{{/*
Return the Redis connection string
*/}}
{{- define "wachd.redisURL" -}}
{{- if .Values.redis.enabled -}}
{{- printf "redis://:%s@%s-redis-master:6379" .Values.redis.auth.password (include "wachd.fullname" .) -}}
{{- else -}}
{{- $protocol := ternary "rediss" "redis" .Values.redis.external.tls -}}
{{- printf "%s://:$(REDIS_PASSWORD)@%s:%d" $protocol .Values.redis.external.host (int .Values.redis.external.port) -}}
{{- end -}}
{{- end }}
