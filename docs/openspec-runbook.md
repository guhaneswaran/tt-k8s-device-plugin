# OpenSpec Runbook

How we use [OpenSpec](https://openspec.pro/) to drive this project spec-first,
plus one-time setup for a fresh machine/account.

OpenSpec is a **spec-driven** workflow: describe *what* you want as a spec, agree
on it, then implement. The loop is **propose → apply → archive**:

- **propose** — write a change proposal (`proposal.md`, delta `specs/`, `tasks.md`) *before* code
- **apply** — implement the tasks
- **archive** — fold the change into the current-truth specs once done

---

## One-time setup

> Status: completed 2026-07-03 (Node v24, OpenSpec 1.5.0). Repeat only on a fresh
> machine or account. Everything is user-scoped — no `sudo` (important: `mcw` is
> a shared account).

```bash
# 1. Node.js via nvm (OpenSpec needs Node >= 20.19)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
exec bash                     # reload shell so nvm loads
nvm install --lts
node --version                # verify >= 20.19

# 2. OpenSpec CLI
npm install -g @fission-ai/openspec@latest
openspec --version

# 3. Initialize in the repo (choose "Claude Code" when prompted)
cd ~/Guhan/k8s-device-plugin
openspec init
```

New shells load Node automatically (nvm appends to `~/.bashrc`). If a tool can't
find `node`, run `source ~/.nvm/nvm.sh` first.

---

## What `init` created

```
openspec/
├── config.yaml        # project context + per-artifact rules (shown to the AI)
├── specs/             # current-truth specs (the source of truth)
└── changes/
    └── archive/       # completed changes land here
.claude/commands/opsx/ # slash commands: explore, propose, apply, archive, sync
```

Both `openspec/` and `.claude/` are committed — the specs live with the code.

---

## Daily workflow

```bash
~/tt-session.sh --login       # SSH/GitHub/Claude + minikube (containerd)
cd ~/Guhan/k8s-device-plugin
# ... work with Claude using the /opsx: commands ...
~/tt-session.sh --logout      # before disconnecting (shared account)
```

### The change loop (run from Claude Code)

| Command | When | What it does |
|---------|------|--------------|
| `/opsx:explore <idea>` | fuzzy idea | Think through options before committing |
| `/opsx:propose <what>` | ready to plan | Creates `openspec/changes/<id>/` with `proposal.md`, delta `specs/`, `tasks.md` |
| *(review)* | before code | **You review the proposal** — this is the agreement point |
| `/opsx:apply` | approved | Implement the tasks |
| `/opsx:archive` | done | Merge the change into `openspec/specs/` (current truth) |
| `/opsx:sync` | as needed | Reconcile specs with reality |

Rule of thumb: **no code before the proposal is reviewed.**

---

## Project plan (how we're using it here)

1. **Baseline** — capture the *current* plugin (discovery, registration, health,
   CDI) as specs in `openspec/specs/`, so the source of truth matches reality.
2. **First change — Prometheus metrics** (Phase-1 roadmap item): full
   propose → review → apply → archive loop.
3. Forward roadmap (NFD, DRA driver) flows through the same loop — see
   [ROADMAP.md](../ROADMAP.md).

The reviewed proposals double as the artifact to show Tenstorrent for feedback.

---

## Notes

- `openspec/config.yaml` holds project context (tech stack, conventions) that the
  AI sees when drafting artifacts — worth filling in early.
- Commit `openspec/` changes normally; no `Co-Authored-By` trailer in this repo.
