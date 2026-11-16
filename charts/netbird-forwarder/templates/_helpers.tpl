{{- define "netbird-forwarder.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "netbird-forwarder.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "netbird-forwarder.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "netbird-forwarder.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "netbird-forwarder.setupKeySecret" -}}
{{- .Values.netbird.setupKey.existingSecret | default (include "netbird-forwarder.fullname" .) -}}
{{- end -}}

{{- define "netbird-forwarder.validate" -}}
{{- $l := .Values.listener -}}
{{- if and (not $l.port) $l.target -}}
{{- fail "listener.port and listener.target must be set together" -}}
{{- end -}}
{{- if and $l.port (not $l.target) -}}
{{- fail "listener.port and listener.target must be set together" -}}
{{- end -}}
{{- if and (not .Values.routes) (not $l.port) -}}
{{- fail "set routes, listener.port and listener.target, or both" -}}
{{- end -}}
{{- if and (not .Values.netbird.setupKey.existingSecret) (not .Values.netbird.setupKey.value) -}}
{{- fail "set netbird.setupKey.existingSecret or netbird.setupKey.value" -}}
{{- end -}}
{{- range $i, $r := .Values.routes -}}
{{- if not (and $r.vip $r.port $r.target) -}}
{{- fail (printf "routes[%d] needs vip, port and target" $i) -}}
{{- end -}}
{{- end -}}
{{- end -}}
