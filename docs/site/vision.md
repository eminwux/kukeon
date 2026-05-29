# Building a Goal Compiler: The Design of Kukeon

*How to run a self-improving software-building organization on a Raspberry Pi — and why every part of it is a diff you can read.*

---

There's a particular feeling you get running coding agents seriously. You have Claude Code in one terminal, a dev server in another, a couple more agents working different tasks, and somewhere in the pile a process died twenty minutes ago and nobody told you. The agents can't see each other. The work lives in your head. And the moment you want any of this to be *trustworthy* — auditable, reproducible, runnable somewhere that isn't your laptop — none of the existing tools help, because they were built to run an agent, not to run an *organization* of them accountably.

Kukeon is an attempt to build that organization properly. It's a self-hosted system where a team of AI agents — a planner, developers, a reviewer, and a meta-agent that improves the others — takes a goal, builds it, reviews it, ships it, operates it, and refines both the product and itself. It runs on a single Linux host you own, from a cloud VM down to a Raspberry Pi. And the organizing idea, which I'll build up to, is that the whole thing behaves like a **bootstrapping compiler**: a system that ultimately builds and improves itself, with the first thing it compiles being itself.

This is a design doc. I'm going to walk through the decisions, including the ones where the obvious approach is wrong, because the decisions are the interesting part.

---

## The first decision: agents are workloads

The starting observation is almost boring once you say it out loud. An AI coding agent is a process that needs a filesystem, scoped credentials, network isolation, and a bounded lifetime. That's a *container workload*. So agents should run on a real container runtime, with real isolation — not in ad-hoc shells where they can see each other's secrets and step on each other's state.

So Kukeon is, at its base, a containerd-native runtime. It defines an explicit hierarchy, and the important thing is that every level maps to a real Linux primitive, not an invented abstraction:

```
Realm → Space → Stack → Cell → Container
```

A **Realm** is a containerd namespace. A **Space** is one CNI network plus one cgroup subtree — this is where default-deny networking lives. A **Stack** groups related cells. A **Cell** is pod-like: one root container owns the network namespace, others join it. A **Container** is just an OCI container inside the cell.

Why the insistence on real primitives? Because the entire value proposition is that you can *trust* the isolation, and you can only trust what you can inspect. Every layer corresponds to something you can examine with tools you already know — `ctr` for the namespace, `ip link` for the network, `ls /sys/fs/cgroup` for the cgroups. There's no proprietary control plane you have to take on faith. The daemon (`kukeond`) is a thin layer over containerd, CNI, and cgroups; `kuke` is the CLI. You declare resources in YAML and the daemon reconciles them.

This is also why it's containerd directly and not Docker. Docker's daemon model and flat networking abstract away exactly the primitives I want precise control over. And keeping the substrate this lean is what lets it run on hardware modest enough to make the sovereignty claim *credible* — more on that later, but the short version is: if it runs on a Raspberry Pi, it can't be hiding a cloud dependency.

---

## How you describe a cell (and why this took three tries)

The runtime runs cells. But how do you *describe* one? This evolved through three generations, and the path is instructive.

The first version, `CellProfile`, was client-side only — the CLI expanded a profile into a cell locally and ran it. It worked, but it duplicated the same machinery into every profile: cloning the repo, setting up git identity, injecting secrets. The lifecycle was invisible to the daemon.

The current model splits the concern in two, and both are daemon-stored:

- A **`CellBlueprint`** is a parametrized *template* — the structure, containers, and setup work, with slots.
- A **`CellConfig`** fills those slots with concrete values and an identity. Blueprint + Config = a runnable cell.

If you've used container orchestration this is a familiar shape — template versus instance, *how to build a thing* versus *this specific thing*. The reason it matters here is that it becomes the substrate for the agent organization: **each agent role is a Blueprint, and each dispatched agent is a Config instantiating it.** The planner role is a template; "the planner working on project X right now" is an instance.

One principle worth pulling out: the setup work a cell does at boot — cloning, git identity, mounting credentials — runs *inside the container*, with the container's own UID and scoped credentials, never on the daemon's host context. The daemon orchestrates; it never touches your SSH keys. That clean trust boundary matters more and more as the system gets more autonomous.

---

## The organization: roles with separation of powers

On top of the runtime sits the team. The design rule is one role per cell, autonomous within a narrow mandate, with hard boundaries on what each *won't* do:

- **PM** decomposes a goal into a plan, grooms the backlog, prioritizes. Never touches code, never merges.
- **Dev** picks a ready task, implements, tests, opens a PR, addresses feedback. Never merges, never manages the backlog.
- **Reviewer** reviews PRs and signals a verdict. Never authors code, never merges.
- **Meta** creates and refines *the other roles' playbooks*, via PRs. Never merges, never touches product code.
- **Orchestrator** is the conductor between you and the agents — dispatches roles to cells, reads their logs. Never authors or merges.

The `will not` column is the actual design. **The author of work is never its approver, and a human does the merging.** This separation of powers is what makes the audit trail mean something. It's also the thing a single "do-everything" self-improving agent structurally cannot give you — a lone agent grading its own work is not an audit. An organization with enforced role boundaries is.

Each recurring task is a small playbook file (a "skill"). These playbooks are, in a real sense, the agents' *source code* — and that turns out to be the key to how the system improves itself. The judgment-heavy skills — how the PM decomposes a goal, how a dev picks which task is actually ready, what the reviewer considers blocking — are where the real intelligence lives, and where self-improvement pays off most.

### The plan is a graph, and that's load-bearing

The PM doesn't write a to-do list; it writes a graph. A goal becomes an initiative (an epic) and a set of nodes (sub-issues) with dependency edges, all expressed as issues in the project's Git forge. Because it's a graph and not a list, independent nodes can be worked *in parallel* by multiple dev cells.

The PM's hardest skill is making that graph *honest* — right-sized nodes, real dependencies, and an accurate read of which nodes are genuinely independent. Everything downstream trusts this structure, so a wrong graph is expensive. Re-partitioning a node that turned out too big or too coupled is a first-class operation. Get the decomposition right and the rest of the system flows; get it wrong and no amount of downstream cleverness saves you.

---

## Concurrency: where the obvious approach is wrong

Now the parallelism bites, and this is my favorite decision in the system because the intuitive answer is a trap.

You're running two dev cells. Both look for work the same way: list the issues, find one without an "in-progress" label, claim it by adding the label, start working. The bug is immediate — both cells read "no label," both decide to claim, both add it, both build the same thing. It's a textbook check-then-act race.

The temptation is to fix it with *better labels* or more careful polling. That's the trap. **A label is not a concurrency primitive.** It's a piece of mutable shared state with no atomic compare-and-swap and no ownership semantics. You're implementing a lock with a sticky note. A per-process guard like `flock` stops two instances on one host, but it does nothing for two cells racing across the system.

The right fix is to separate two things that labels had conflated: *where the work is described* and *who is allowed to work it right now*. The description stays in the forge. The claim moves into the daemon, as a **lease**:

```
key:    <repo>#<node-id>      ← note: forge-agnostic
holder: <cell-id>
acquired_at, expires_at
```

The claim is the one operation labels can't do: an **atomic compare-and-swap** — create the lease if and only if no live lease exists for that key. Exactly one cell wins; the other moves on. Clean.

Two things make this *correct*, not just convenient:

1. **The daemon already knows liveness.** It owns cell lifecycle, so when a cell dies, its lease is released — not on a hopeful timeout, but on the actual death signal. This is the deep reason the lease belongs in the runtime and nowhere else: only the daemon has *both* the claim and the liveness information. Put the lease in the forge and you've separated them, and you're back to guessing.
2. **Heartbeat plus a TTL backstop.** A working cell renews its lease; silence lets it expire. That covers the cases the death signal might miss — a hang, a partition.

Labels don't disappear — but they get demoted to what they're good at: a *human-visible reflection* of state, written *after* a lease transition, never the claim mechanism itself.

And notice the bonus in that lease key: it's forge-agnostic. The concurrency logic doesn't know or care whether the work item is a GitHub issue or a Forgejo one. Which leads to the next decision.

---

## Where the truth lives: not in Kukeon

Here's a decision that looks like a limitation and is actually the whole point. Kukeon does *not* store the issues, the plan, or the audit trail in its own database. The Git forge is the system of record — GitHub for convenience, or self-hosted Forgejo/GitLab for air-gapped use. Kukeon keeps only references and a derived operational view: the plan-graph as a typed projection, the lease table, a cache so agents can read without hammering the forge's rate limits.

Why give up owning your own data model? Because of what you're building. This is a system that acts autonomously and *rewrites its own behavior*. The record of what such a system did, and how it changed itself, should live **outside the system being audited** — in infrastructure you already trust, with signed, attributed history. "The audit log is in our daemon's database, trust us" is a weak story to tell a security reviewer. "Every action is a commit and a PR in your own Git, signed and attributed" is a strong one.

And it's not a sacrifice of sovereignty, because the answer to "but then you depend on GitHub" is: self-host the forge. A self-hosted Forgejo gives you Git's content-addressed, append-mostly history — the strongest audit substrate that exists — on your own hardware, air-gapped if you need it. You'd be foolish to rebuild that badly in a native store. The right move isn't to escape the forge; it's to *own* the forge.

This is why the forge-agnostic lease key matters. The coordination logic is already independent of the forge. Put a thin adapter behind the forge calls and "works only with GitHub" stops being true at the layer that counts — swap in Forgejo for air-gap and the loops don't change a line.

---

## The loops: what Kukeon actually is

Everything so far is scaffolding. The thing that makes Kukeon *Kukeon* is that it closes loops. There are three, and the discipline is that they have separate intakes and separate destinations.

### The work loop

The core cycle, every artifact a Git object:

```
goal → PM builds the plan-graph → dev claims a ready, unleased node →
implements → opens PR → reviewer gates → (changes? → dev fixes) →
human merges → release when the graph completes → next goal
```

You, the human, are in exactly two places: stating the goal, and merging. Everything in between runs on its own. That merge gate is not friction to be optimized away — it's the feature. It's what makes the autonomy *accountable*.

### Loop A — the system improves its own agents

Every agent runs a reflection step when it finishes a task (on an ending hook, so it's structural, not something an agent might forget). If the task surfaced a durable lesson — "this kind of node keeps getting sized wrong," "this rule slowed me down" — the agent files a `learn-feedback` issue in the *agents* repo. The meta-agent picks it up, implements the playbook change as a PR, and a human merges it.

This is the answer to every vague claim about "self-improving agents." The unit of learning is a playbook file. The mutation is a pull request. A human approves it. **Improvement is a diff you can read** — not a weight update you can't, not a memory mutation you can't see. The system gets better the way a good team does: through reflected lessons and reviewed changes.

### Loop B — the system improves the product

The same reflection step asks a *second, independent* question: did this task surface a verified bug in the *product* (not the process)? If so, it files an issue in the *project* repo, and that re-enters the work loop.

The independence is the point. Loop A refines the builders; Loop B refines the thing being built. They write to different repos and they cannot cross: a process lesson can never edit product code, a product bug can never edit an agent's playbook. That firewall is what keeps "it improves itself" from becoming an unaccountable tangle.

### Loop C — the product reports its own failures

Once something is built and running, it emits errors. Loop C closes that:

```
running product → error sink (groups & counts; plain code, no LLM) →
a threshold or schedule wakes a live-debugger agent →
agent triages the grouped batch, dedups, files a project-repo issue → work loop
```

The design subtlety: no agent sits *tailing* logs — that would burn tokens constantly deciding "nothing happened." The cheap deterministic sink does the high-volume collecting and grouping; an agent is dispatched only when there's a batch worth a judgment call. And like Loop B, it files only to the project repo — a runtime error is a fact about the product, never a lesson about the process. The daemon might route the error stream, but it never files issues itself: filing needs triage judgment and bot attribution, and those belong to an agent. **Infrastructure never files; agents file.** That single rule is what keeps the three loops' write-targets clean.

---

## The frame: a bootstrapping compiler

Step back and the shape of the whole thing snaps into focus, and it's a precise analogy rather than a loose one.

A compiler *bootstraps* when you use it to compile itself. You write a minimal seed compiler — *stage-0* — in some *other* language, because nothing exists yet to compile the real one. You use stage-0 to compile a better compiler written in the target language. Then that compiles the next. Improvements compound, because each better compiler builds the next one. The thing builds the thing that builds the thing.

Kukeon maps onto this exactly:

- **Stage-0 is you.** Right now the human authors the playbooks and approves every merge. The system can't yet produce itself without you — just as a language can't compile its first compiler in itself. The seed is, by necessity, foreign.
- **The source language is the agent playbooks.** Loop A is the system *recompiling its own source*. The meta-agent is literally the part of the compiler that compiles the compiler.
- **Self-hosting is the loop closing without you in the critical path** — and the way you prove it is a *fixpoint*: the system builds a version of itself, that version builds the next, and the builds *converge* instead of drifting. "Kukeon built Kukeon, and successive self-builds are stable" is a far stronger claim than any list of features.

The analogy comes with two famous warnings, and the architecture is built around both:

**Trusting-trust.** Ken Thompson's classic result: a self-hosting compiler can carry a flaw that reproduces into every version it compiles, *invisibly*, because the corruption lives in the compiling compiler, not in the clean-looking source. Kukeon's version of this is sharp — it's the meta-agent evaluating changes to its *own* judgment. A bad refinement to how the system evaluates refinements would propagate into all future evaluations, and every diff would still look clean. The defense is exactly the human merge gate: a trusted stage-0 that a corrupted stage-N cannot certify away. This is *why* the human stays in the loop on purpose, not as a temporary limitation.

**Divergence.** A miscompiled compiler can produce a compiler that produces garbage, and if you've dropped the seed, you can't get back. The mitigations are the compiler-builder's: keep the seed (stay stage-0 longer than feels necessary), keep every intermediate stage reproducible (the Git trail *is* the reproducible build log), and test the fixpoint before trusting it.

This is the test the whole design is pointed at — and it's why the most-requested feature, an agent that merges autonomously and removes the human, is *deliberately deferred*. Even the most aggressive teams in this space keep a human on the merge. You don't drop the seed until self-hosting is proven stable. The drink separates if you stop stirring too early.

Which, finally, is why the name is what it is. Heraclitus wrote that the *kykeon* — a barley drink — separates if it isn't stirred. He meant it as an image of the *logos*: order isn't a static state, it's a process held together by motion. A bootstrapping compiler has exactly that nature. It isn't an artifact; it's a process that stays itself only by continuously running on itself. Kukeon is order through motion — it becomes what it is only while it keeps building and refining itself.

---

## Where it stands

Kukeon is beta. The runtime, daemon, and hierarchy are stable for single-host use. The agent organization and Loops A and B run today, through human-merged PRs. The Blueprint/Config model is actively replacing CellProfile. Leases, the projection/cache layer, the typed plan-graph, and Loop C are the active frontier. The autonomous approver — the one that would remove the human merge gate — is deliberately on the far side of "prove the fixpoint first."

Built so far: the runtime and hierarchy; daemon lifecycle; the agent roles and skills; reflection-on-hook; process learning (Loop A) and product-defect feedback (Loop B). In flight: Blueprint/Config, leases, the projection layer, the plan-graph as a typed object, runtime feedback (Loop C). Deliberately not built yet: anything that takes the human off the merge.

If the design has a single thesis, it's this: **autonomy and accountability aren't in tension if every action the system takes — including the actions it takes to improve itself — is an owned, inspectable, human-approved diff.** The agents do the work. The forge holds the truth. The human holds the seed. And the whole thing is itself only while it's being stirred.

---

*Kukeon is open-source (Apache 2.0), built in the open at github.com/eminwux/kukeon.*
