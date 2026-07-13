import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  type AgentConfig,
  type AgentConfigUpdate,
  type ServerSettings,
} from "../api/client";
import { ErrorBox, PageTitle, Spinner } from "../components/ui";
import discordIcon from "../assets/discord.png";
import slackIcon from "../assets/slack.png";
import teamsIcon from "../assets/teams.png";

const INTERVALS = ["1m", "2m", "5m", "10m", "15m", "30m", "1h"];

const PROVIDERS: { id: string; label: string }[] = [
  { id: "ollama", label: "Ollama (local models, free)" },
  { id: "anthropic", label: "Anthropic Claude" },
  { id: "openai", label: "OpenAI (ChatGPT)" },
  { id: "deepseek", label: "DeepSeek" },
  { id: "gemini", label: "Google Gemini" },
];

const KEY_PLACEHOLDERS: Record<string, string> = {
  anthropic: "sk-ant-...",
  openai: "sk-...",
  deepseek: "sk-...",
  gemini: "AIza...",
};

export default function SettingsPage() {
  const queryClient = useQueryClient();

  const configQuery = useQuery({ queryKey: ["agent-config"], queryFn: api.agentConfig });
  const serverQuery = useQuery({ queryKey: ["server-settings"], queryFn: api.serverSettings });

  return (
    <div className="mx-auto max-w-2xl">
      <PageTitle>Settings</PageTitle>

      {configQuery.isLoading && <Spinner label="Loading agent configuration..." />}
      {configQuery.error != null && (
        <ErrorBox
          title="Could not load agent configuration"
          message={(configQuery.error as Error).message + " — is the agent service running?"}
        />
      )}

      {configQuery.data && (
        <AgentSettingsForm
          config={configQuery.data}
          persistHint={serverQuery.data?.settingsPersist ?? false}
          onSaved={() => {
            queryClient.invalidateQueries({ queryKey: ["agent-config"] });
            queryClient.invalidateQueries({ queryKey: ["agent-status"] });
          }}
        />
      )}

      {serverQuery.data && <ServerInfoCard settings={serverQuery.data} />}
    </div>
  );
}

function AgentSettingsForm({
  config,
  persistHint,
  onSaved,
}: {
  config: AgentConfig;
  persistHint: boolean;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<AgentConfigUpdate>({
    provider: config.provider,
    model: config.model,
    ollamaHost: config.ollamaHost,
    reviewInterval: config.reviewInterval,
    monitorEnabled: config.monitorEnabled,
    apiKey: "",
    notificationsEnabled: config.notificationsEnabled,
    discordWebhookUrl: "",
    slackWebhookUrl: "",
    teamsWebhookUrl: "",
    notifyMinSeverity: config.notifyMinSeverity || "warning",
    prometheusUrl: config.prometheusUrl,
  });
  const [savedNote, setSavedNote] = useState("");

  // Re-sync the form if the config is refetched (e.g. after save).
  useEffect(() => {
    setForm((f) => ({
      ...f,
      provider: config.provider,
      model: config.model,
      ollamaHost: config.ollamaHost,
      reviewInterval: config.reviewInterval,
      monitorEnabled: config.monitorEnabled,
      notificationsEnabled: config.notificationsEnabled,
      notifyMinSeverity: config.notifyMinSeverity || "warning",
      prometheusUrl: config.prometheusUrl,
    }));
  }, [config]);

  const mutation = useMutation({
    mutationFn: api.updateAgentConfig,
    onSuccess: (result) => {
      setForm((f) => ({
        ...f,
        apiKey: "",
        discordWebhookUrl: "",
        slackWebhookUrl: "",
        teamsWebhookUrl: "",
      }));
      setSavedNote(
        result.persisted
          ? "Applied and persisted — the settings survive pod restarts."
          : result.persistError
            ? `Applied live, but persisting failed: ${result.persistError}`
            : "Applied live. (Running outside Kubernetes: settings reset to env values on restart.)",
      );
      onSaved();
      setTimeout(() => setSavedNote(""), 8000);
    },
  });

  const set = <K extends keyof AgentConfigUpdate>(key: K, value: AgentConfigUpdate[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const isCloud = form.provider !== "ollama";
  const keyConfigured = config.apiKeysSet?.[form.provider] ?? false;

  const intervalOptions = INTERVALS.includes(normalizeDuration(form.reviewInterval))
    ? INTERVALS
    : [normalizeDuration(form.reviewInterval), ...INTERVALS];

  return (
    <form
      className="card mb-6 space-y-4 p-5"
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate({ ...form, reviewInterval: normalizeDuration(form.reviewInterval) });
      }}
    >
      <div className="flex items-center justify-between">
        <h2 className="font-semibold">AI Agent</h2>
        <span className="text-xs text-slate-400">
          changes apply immediately{persistHint ? " and persist to the ConfigMap" : ""}
        </span>
      </div>

      <Field label="LLM provider">
        <select
          className="input w-full"
          value={form.provider}
          onChange={(e) =>
            // Models belong to a provider — changing provider resets the
            // model to the provider default instead of carrying it over.
            setForm((f) => ({ ...f, provider: e.target.value, model: "", apiKey: "" }))
          }
        >
          {PROVIDERS.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
        </select>
      </Field>

      <ModelField
        provider={form.provider}
        ollamaHost={form.ollamaHost ?? ""}
        value={form.model}
        onChange={(model) => set("model", model)}
      />

      {form.provider === "ollama" && (
        <Field label="Ollama host">
          <input
            className="input w-full"
            value={form.ollamaHost}
            onChange={(e) => set("ollamaHost", e.target.value)}
          />
        </Field>
      )}

      {isCloud && (
        <Field
          label={`API key (${form.provider})`}
          hint={
            keyConfigured
              ? "A key is configured for this provider. Leave blank to keep it; paste a new one to replace it. Keys are write-only — never shown."
              : "No key configured for this provider yet — required."
          }
        >
          <input
            className="input w-full"
            type="password"
            autoComplete="off"
            placeholder={
              keyConfigured ? "•••••••• (configured)" : (KEY_PLACEHOLDERS[form.provider] ?? "...")
            }
            value={form.apiKey}
            onChange={(e) => set("apiKey", e.target.value)}
          />
        </Field>
      )}

      <div className="grid grid-cols-2 gap-4">
        <Field label="Review interval" hint="How often the cluster review runs.">
          <select
            className="input w-full"
            value={normalizeDuration(form.reviewInterval)}
            onChange={(e) => set("reviewInterval", e.target.value)}
          >
            {intervalOptions.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Periodic review" hint="Off = no background LLM calls; chat still works.">
          <label className="flex h-9 items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.monitorEnabled}
              onChange={(e) => set("monitorEnabled", e.target.checked)}
            />
            enabled
          </label>
        </Field>
      </div>

      <div className="border-t border-slate-200 pt-4 dark:border-slate-800">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="font-semibold">Notifications</h3>
          <span className="text-xs text-slate-400">alerts on status changes, not every review</span>
        </div>

        <div className="space-y-4">
          <Field label="Notifications">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.notificationsEnabled}
                onChange={(e) => set("notificationsEnabled", e.target.checked)}
              />
              enabled — sends to every channel with a webhook configured below
            </label>
          </Field>

          <WebhookField
            icon={discordIcon}
            label="Discord webhook"
            configured={config.discordWebhookSet}
            placeholder="https://discord.com/api/webhooks/..."
            hint="Channel settings ⚙ → Integrations → Webhooks → New Webhook."
            value={form.discordWebhookUrl ?? ""}
            onChange={(v) => set("discordWebhookUrl", v)}
          />
          <WebhookField
            icon={slackIcon}
            label="Slack webhook"
            configured={config.slackWebhookSet}
            placeholder="https://hooks.slack.com/services/..."
            hint="Slack app → Incoming Webhooks → Add New Webhook to Workspace."
            value={form.slackWebhookUrl ?? ""}
            onChange={(v) => set("slackWebhookUrl", v)}
          />
          <WebhookField
            icon={teamsIcon}
            label="Teams webhook"
            configured={config.teamsWebhookSet}
            placeholder="https://... (Workflows webhook URL)"
            hint='Teams channel → Workflows → "Post to a channel when a webhook request is received".'
            value={form.teamsWebhookUrl ?? ""}
            onChange={(v) => set("teamsWebhookUrl", v)}
          />

          <div className="grid grid-cols-2 gap-4">
            <Field label="Notify from severity" hint="critical = only page for outages.">
              <select
                className="input w-full"
                value={form.notifyMinSeverity}
                onChange={(e) => set("notifyMinSeverity", e.target.value)}
              >
                <option value="warning">warning and above</option>
                <option value="critical">critical only</option>
              </select>
            </Field>
            <Field label="Verify channels" hint="Save first if you just pasted a URL.">
              <TestNotificationButton
                anyConfigured={
                  config.discordWebhookSet || config.slackWebhookSet || config.teamsWebhookSet
                }
              />
            </Field>
          </div>
        </div>
      </div>

      <div className="border-t border-slate-200 pt-4 dark:border-slate-800">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="font-semibold">Metrics</h3>
          <span className="text-xs text-slate-400">
            gives the agent CPU/memory/throttling visibility
          </span>
        </div>

        <div className="grid grid-cols-[1fr_auto] items-end gap-4">
          <Field
            label="Prometheus URL"
            hint="The bundled in-cluster Prometheus, or your existing one. Empty = metrics tools disabled."
          >
            <input
              className="input w-full"
              placeholder="http://prometheus.kentinel.svc:9090"
              value={form.prometheusUrl}
              onChange={(e) => set("prometheusUrl", e.target.value)}
            />
          </Field>
          <MetricsHealthButton configured={config.prometheusUrl !== ""} />
        </div>
      </div>

      {mutation.error != null && (
        <ErrorBox title="Save failed" message={(mutation.error as Error).message} />
      )}
      {savedNote && <div className="text-sm text-emerald-600">✓ {savedNote}</div>}

      <button className="btn-primary" type="submit" disabled={mutation.isPending}>
        {mutation.isPending ? "Applying..." : "Save settings"}
      </button>
    </form>
  );
}

function WebhookField({
  icon,
  label,
  configured,
  placeholder,
  hint,
  value,
  onChange,
}: {
  icon: string;
  label: string;
  configured: boolean;
  placeholder: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <label className="mb-1 flex items-center gap-1.5 text-sm font-medium">
        <img src={icon} alt="" className="h-4 w-4" />
        {label}
        {configured && <span className="text-xs font-normal text-emerald-600">configured</span>}
      </label>
      <input
        className="input w-full"
        type="password"
        autoComplete="off"
        placeholder={configured ? "•••••••• (configured — blank keeps it)" : placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="mt-1 text-xs text-slate-400">{hint}</p>
    </div>
  );
}

function TestNotificationButton({ anyConfigured }: { anyConfigured: boolean }) {
  const test = useMutation({ mutationFn: api.testNotification });
  return (
    <div>
      <button
        type="button"
        className="btn-ghost border border-slate-300 dark:border-slate-700"
        disabled={!anyConfigured || test.isPending}
        onClick={() => test.mutate()}
      >
        {test.isPending ? "Sending..." : "Send test notification"}
      </button>
      {test.isSuccess && (
        <p className="mt-1 text-xs text-emerald-600">✓ Sent to all configured channels</p>
      )}
      {test.error != null && (
        <p className="mt-1 break-words text-xs text-red-500">{(test.error as Error).message}</p>
      )}
    </div>
  );
}

function MetricsHealthButton({ configured }: { configured: boolean }) {
  const test = useMutation({ mutationFn: api.metricsHealth });
  return (
    <div className="pb-0.5">
      <button
        type="button"
        className="btn-ghost border border-slate-300 dark:border-slate-700"
        disabled={!configured || test.isPending}
        onClick={() => test.mutate()}
        title={configured ? "Query Prometheus to verify connectivity" : "Save a URL first"}
      >
        {test.isPending ? "Checking..." : "Test connection"}
      </button>
      {test.isSuccess && <p className="mt-1 text-xs text-emerald-600">✓ Prometheus reachable</p>}
      {test.error != null && (
        <p className="mt-1 max-w-xs break-words text-xs text-red-500">
          {(test.error as Error).message}
        </p>
      )}
    </div>
  );
}

// ModelField renders a dropdown of selectable models: Ollama = the models
// actually installed on the server, cloud providers = a curated list.
// Falls back to free-text input when the list can't be fetched.
function ModelField({
  provider,
  ollamaHost,
  value,
  onChange,
}: {
  provider: string;
  ollamaHost: string;
  value: string;
  onChange: (model: string) => void;
}) {
  const modelsQuery = useQuery({
    queryKey: ["agent-models", provider, ollamaHost],
    queryFn: () => api.agentModels(provider, provider === "ollama" ? ollamaHost : undefined),
    staleTime: 30_000,
    retry: false,
  });

  const defaultModel = modelsQuery.data?.default ?? "";
  const models = modelsQuery.data?.models ?? [];
  const listUnavailable =
    modelsQuery.isError || (modelsQuery.data != null && models.length === 0);

  if (listUnavailable) {
    const reason =
      modelsQuery.data?.error ??
      (modelsQuery.error as Error | undefined)?.message ??
      "no models found";
    return (
      <Field
        label="Model"
        hint={`Could not list models (${reason}) — enter a model name manually.`}
      >
        <input
          className="input w-full"
          value={value}
          placeholder={defaultModel}
          onChange={(e) => onChange(e.target.value)}
        />
      </Field>
    );
  }

  // Keep a previously saved model selectable even if it's not in the list.
  const options = value && !models.includes(value) ? [value, ...models] : models;

  return (
    <Field
      label="Model"
      hint={
        provider === "ollama"
          ? "Models installed on the Ollama server. Must be tool-calling capable (qwen3, llama3.1, mistral-nemo)."
          : "Curated list, best first — any valid model ID also works (type it after picking Custom via the fallback)."
      }
    >
      <select
        className="input w-full"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={modelsQuery.isLoading}
      >
        <option value="">{`Provider default${defaultModel ? ` (${defaultModel})` : ""}`}</option>
        {options.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
    </Field>
  );
}

function ServerInfoCard({ settings }: { settings: ServerSettings }) {
  return (
    <div className="card p-5">
      <h2 className="mb-3 font-semibold">Server (read-only)</h2>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
        <InfoItem label="Running mode" value={settings.inCluster ? `in-cluster (${settings.namespace})` : "outside Kubernetes"} />
        <InfoItem label="Agent URL" value={settings.agentUrl} />
        <InfoItem label="Static dir" value={settings.staticDir || "(dev mode — Vite serves the UI)"} />
        <InfoItem label="Settings persistence" value={settings.settingsPersist ? "ConfigMap write-back" : "runtime only"} />
      </dl>
      <p className="mt-3 text-xs text-slate-400">
        Server parameters (port, RBAC, agent URL) are deployment-level — change them in the
        manifests / compose file. See docs/configuration.md.
      </p>
    </div>
  );
}

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-semibold uppercase tracking-wide text-slate-400">{label}</dt>
      <dd className="mt-0.5 break-all">{value}</dd>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-sm font-medium">{label}</label>
      {children}
      {hint && <p className="mt-1 text-xs text-slate-400">{hint}</p>}
    </div>
  );
}

// normalizeDuration maps Go's "5m0s" back to "5m" so it matches the select options.
function normalizeDuration(d: string): string {
  return d.replace(/^(\d+[hm])0s$/, "$1").replace(/^(\d+h)0m$/, "$1");
}
