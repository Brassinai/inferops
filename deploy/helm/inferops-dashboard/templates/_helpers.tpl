{{- define "inferops-dashboard.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "inferops-dashboard.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains (include "inferops-dashboard.name" .) .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "inferops-dashboard.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "inferops-dashboard.labels" -}}
app.kubernetes.io/name: {{ include "inferops-dashboard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "inferops-dashboard.selectorLabels" -}}
app.kubernetes.io/name: {{ include "inferops-dashboard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "inferops-dashboard.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "inferops-dashboard.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
