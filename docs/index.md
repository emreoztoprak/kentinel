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

## See it

![Kentinel dashboard — the AI cluster review flagging a broken image tag, a pending pod, and a crash loop, each with a concrete recommendation](/screenshots/kentinel-dashboard-dark.png)

The staged demo above: four incidents deployed with `make demo-incident`,
every one found and explained by the review loop — including the typo in
`nginx:1.27-alpin`.

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
