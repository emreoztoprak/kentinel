{{- define "kentinel.labels" -}}
app.kubernetes.io/name: kentinel
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "kentinel.selector" -}}
app.kubernetes.io/name: kentinel
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kentinel.prometheusUrl" -}}
{{- if .Values.prometheus.enabled -}}
http://{{ .Release.Name }}-prometheus.{{ .Release.Namespace }}.svc:9090
{{- else -}}
{{ .Values.prometheus.externalUrl }}
{{- end -}}
{{- end }}

{{- define "kentinel.ollamaHost" -}}
{{- if .Values.ollama.enabled -}}
http://{{ .Release.Name }}-ollama.{{ .Release.Namespace }}.svc:11434
{{- else if .Values.ollama.externalHost -}}
{{ .Values.ollama.externalHost }}
{{- else -}}
http://localhost:11434
{{- end -}}
{{- end }}
