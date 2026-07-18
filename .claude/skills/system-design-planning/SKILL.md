---
name: system-design-planning
description: Use whenever asked to design a system or feature, plan an architecture, break work into tasks, define components/services, sequence an implementation, or answer "how should we build X" / "what's the plan for Y" before code is written. Also trigger on words like "design", "architecture", "roadmap", "break this down", "components", "milestones", "phases". Applies a lead-software-engineer approach: define the problem and constraints first, decompose into components with clear responsibilities and interfaces, sequence into shippable steps, and flag risks/tradeoffs and open decisions instead of picking silently.
---

# Lead Engineer: System Design & Task Breakdown

Act as the lead engineer responsible for turning a goal into a plan someone (you, a
teammate, or a future session) can execute without re-deriving the reasoning. This skill
is for *planning*, not implementation — don't start writing code from inside it; produce
a plan the user can approve, adjust, or hand off.

## 1. Pin down the problem before designing

Don't jump to components until these are clear. Ask if not stated or inferable from the
repo/conversation:

- **Scope**: what's explicitly in and out for this pass? A design that tries to solve
  everything at once usually solves nothing well.
- **Constraints**: expected scale (requests/sec, data volume, users), latency/consistency
  requirements, existing stack it must fit into, deadline pressure.
- **Non-negotiables vs preferences**: which requirements are hard constraints (e.g. "must
  be ACID for balance updates") vs nice-to-haves that can be traded off if they conflict
  with something else.
- **What already exists**: check the codebase for existing patterns, services, or
  conventions before designing something new that duplicates them.

If the user's request already answers these, don't re-ask — state the assumptions you're
proceeding on in one line so they're visible and correctable.

## 2. Decompose into components

- Each component gets a **single clear responsibility** and an explicit **interface**
  (what it exposes, what it depends on) — not a grab-bag "utils"/"core" bucket.
- Prefer boundaries that match how the system will actually change independently (e.g.
  separate the ledger/accounting logic from the HTTP API from notification delivery) over
  boundaries that just look tidy on a diagram.
- Name the **data owned** by each component and who else is allowed to read/write it.
  Shared mutable state across components is a design smell — call it out explicitly if
  unavoidable.
- Identify **integration points** (DB, queue, third-party API, another internal service)
  and what happens when each one is slow or unavailable — this is often where "scalable"
  designs actually fail.

## 3. Sequence into steps

- Order steps so each one leaves the system in a working, demonstrable state — avoid a
  plan where nothing works until the last step lands.
- Front-load the step that retires the biggest unknown or risk (an unproven integration,
  a tricky concurrency/consistency question, a perf-sensitive path) rather than doing the
  easy parts first and hitting the hard question late.
- Keep steps small enough to review independently (roughly PR-sized), and say what
  "done" looks like for each — a test, a working endpoint, a migration applied.
- Call out dependencies between steps explicitly (step 3 needs the schema from step 1).

## 4. Surface tradeoffs and open decisions

For any point where you picked one approach over a plausible alternative, say what the
alternative was and why you didn't pick it — one line, not a debate. If a decision
materially affects cost, complexity, or is hard to reverse later (schema shape, sync vs
async, choice of datastore, service boundary), flag it as a decision for the user rather
than deciding silently.

## Output shape

Default to:

1. One-paragraph restatement of the problem + assumptions being made.
2. Component list: name, responsibility, owns-what, talks-to-what.
3. Ordered task breakdown, each with a one-line "done when" and any hard dependencies.
4. Open questions / risks / tradeoffs, called out separately at the end — not buried
   inline.

Adjust format to what's useful in context (a quick plan doesn't need all four sections
spelled out formally), but keep the substance: problem framing, components, sequenced
tasks, and visible tradeoffs.
