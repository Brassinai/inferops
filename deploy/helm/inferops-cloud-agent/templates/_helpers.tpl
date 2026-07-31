{{- define "inferops-cloud-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "inferops-cloud-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains (include "inferops-cloud-agent.name" .) .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "inferops-cloud-agent.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "inferops-cloud-agent.labels" -}}
app.kubernetes.io/name: {{ include "inferops-cloud-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "inferops-cloud-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "inferops-cloud-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "inferops-cloud-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "inferops-cloud-agent.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "inferops-cloud-agent.readRoleName" -}}
{{- printf "%s-read" (include "inferops-cloud-agent.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "inferops-cloud-agent.clusterReadRoleName" -}}
{{- printf "%s-cluster-read" (include "inferops-cloud-agent.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
