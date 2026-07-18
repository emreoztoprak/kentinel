---
layout: home

hero:
  name: Kentinel
  text: The AI sentinel for your Kubernetes cluster
  tagline: A modern console with a built-in AI agent that continuously reviews your cluster, alerts you when something breaks, answers questions — and, with your approval, fixes things.
  image:
    src: /logo.png
    alt: Kentinel
  actions:
    - theme: brand
      text: Get Started
      link: /deployment
    - theme: alt
      text: View on GitHub
      link: https://github.com/emreoztoprak/kentinel

features:
  - icon: 🤖
    title: AI cluster review
    details: An agent reviews the cluster every few minutes and shows a healthy / warning / critical verdict with concrete findings and recommendations — history persisted, trends on the dashboard.
  - icon: 💬
    title: AI assistant
    details: Ask "why is payments-api failing? check its logs" — the agent inspects resources, logs, events, and metrics with read-only tools and streams a grounded answer.
  - icon: ✅
    title: Approval-gated fixes
    details: In assisted mode the agent proposes a change as a reviewable diff, right in the chat. You approve, the server applies. Never autonomous — enforced by RBAC.
  - icon: 🔒
    title: Local-first LLMs
    details: Bundled Ollama by default — no API keys, no cluster data leaves your infrastructure. Or switch to Claude, OpenAI, DeepSeek, or Gemini from the UI.
  - icon: 🔔
    title: Notifications
    details: Discord, Slack, and Teams alerts on status transitions (and recoveries). Transition-based, never spammy, with a configurable severity threshold.
  - icon: 🧰
    title: Full console
    details: Resource browser, Monaco YAML editing, log tailing, in-browser pod terminal, events, Prometheus-backed metrics — dark mode included.
---

<script setup>
import { ref } from 'vue'
import { withBase } from 'vitepress'

const track = ref(null)
const current = ref(0)

const slides = [
  { img: '/screenshots/kentinel-dashboard-light.png',
    alt: 'Dashboard with the AI cluster review flagging incidents',
    caption: 'The AI review flags a typo’d image tag, an unschedulable pod, and a crash loop — each with a concrete recommendation (staged with make demo-incident).' },
  { img: '/screenshots/kentinel-assistant-light.png',
    alt: 'AI assistant chat',
    caption: 'Ask anything about the cluster — the assistant investigates with read-only tools and streams a grounded answer.' },
  { img: '/screenshots/kentinel-history-light.png',
    alt: 'AI review history',
    caption: 'Every review is persisted: filter by status, see when an incident started and when it recovered.' },
  { img: '/screenshots/kentinel-pods-light.png',
    alt: 'Pod browser',
    caption: 'A full resource browser underneath: pods, deployments, YAML editing, logs, terminal, events.' },
  { img: '/screenshots/kentinel-events-light.png',
    alt: 'Cluster events',
    caption: 'Cluster events with namespace and type filters.' },
  { img: '/screenshots/kentinel-settings-light.png',
    alt: 'Settings page',
    caption: 'Switch between five LLM providers, wire up Discord/Slack/Teams alerts and the daily report — all from the UI, persisted encrypted.' },
]

function onScroll() {
  const el = track.value
  if (el) current.value = Math.round(el.scrollLeft / el.clientWidth)
}
function go(dir) {
  const el = track.value
  if (!el) return
  const next = Math.min(Math.max(current.value + dir, 0), slides.length - 1)
  el.scrollTo({ left: next * el.clientWidth, behavior: 'smooth' })
}
</script>

## See it

<div class="shots">
  <div class="shots-track" ref="track" @scroll.passive="onScroll">
    <figure class="shots-slide" v-for="s in slides" :key="s.img">
      <img :src="withBase(s.img)" :alt="s.alt" loading="lazy" />
      <figcaption>{{ s.caption }}</figcaption>
    </figure>
  </div>
  <button class="shots-btn prev" aria-label="Previous screenshot" :disabled="current === 0" @click="go(-1)">‹</button>
  <button class="shots-btn next" aria-label="Next screenshot" :disabled="current === slides.length - 1" @click="go(1)">›</button>
  <div class="shots-dots">
    <span v-for="(s, i) in slides" :key="i" :class="{ active: i === current }" />
  </div>
</div>

<style scoped>
.shots { position: relative; margin: 16px 0; }
.shots-track {
  display: flex;
  overflow-x: auto;
  scroll-snap-type: x mandatory;
  scrollbar-width: none;
  border-radius: 12px;
}
.shots-track::-webkit-scrollbar { display: none; }
.shots-slide {
  flex: 0 0 100%;
  scroll-snap-align: start;
  margin: 0;
}
.shots-slide img {
  width: 100%;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  display: block;
}
.shots-slide figcaption {
  padding: 8px 4px 0;
  font-size: 13px;
  color: var(--vp-c-text-2);
  text-align: center;
  min-height: 3.2em;
}
.shots-btn {
  position: absolute;
  top: 42%;
  transform: translateY(-50%);
  width: 36px; height: 36px;
  border-radius: 50%;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  font-size: 20px; line-height: 1;
  cursor: pointer;
  opacity: 0.85;
  transition: opacity 0.2s;
}
.shots-btn:hover:not(:disabled) { opacity: 1; }
.shots-btn:disabled { opacity: 0.25; cursor: default; }
.shots-btn.prev { left: 10px; }
.shots-btn.next { right: 10px; }
.shots-dots { display: flex; justify-content: center; gap: 6px; margin-top: 6px; }
.shots-dots span {
  width: 7px; height: 7px; border-radius: 50%;
  background: var(--vp-c-divider);
  transition: background 0.2s;
}
.shots-dots span.active { background: var(--vp-c-brand-1); }
</style>

And the assisted-mode loop end to end — the assistant diagnoses the broken
checkout pod, proposes the fix as a reviewable diff, and after an inline
human approval the server applies it and verifies the rollout:

![Diagnose, propose, approve, fixed — the assisted-mode remediation flow](/screenshots/kentinel-approve-fix.gif)

## Quickstart

Runs local-first out of the box — a bundled Ollama and a bundled minimal
Prometheus, no API keys required:

```sh
helm install kentinel oci://ghcr.io/emreoztoprak/charts/kentinel \
  -n kentinel --create-namespace

kubectl -n kentinel port-forward svc/kentinel-server 8080:80
# open http://localhost:8080
```

Prefer Docker on your laptop? See [Deployment](/deployment) — a single
`docker compose up` works against any cluster your kubeconfig can reach.

By default Kentinel installs in **read-only mode**: it observes and answers
questions but cannot change any resource. Deploy with `--set mode=assisted`
to enable approval-gated remediation, manifest editing, and the pod
terminal — details in [Security](/security).

## Try it: break a shop, watch the AI find it

A demo microservice stack ships with the repo — deploy it healthy, then
break it in four realistic ways:

```sh
make demo-up        # healthy "shop" namespace: storefront, orders-api, cache
make demo-incident  # four distinct incidents appear
# watch the dashboard flag them, then ask the assistant:
#   "why is payments-api failing? check its logs"
make demo-reset     # back to green (recovery notification!)
```
