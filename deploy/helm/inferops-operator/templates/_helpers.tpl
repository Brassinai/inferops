{{- define "inferops-operator.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "inferops-operator.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "inferops-operator.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
