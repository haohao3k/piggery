---
name: piggery-three-review
description: Prepare context and run Piggery three-arm review for a frozen code candidate or recent development lanes and decisions when the user requests that review.
---

# Piggery three-arm review

Use the user's scope text, if supplied. With no extra text, investigate the current project's
recent development lanes, changes and decisions and establish a defensible review boundary.

Run `piggery skills three-review` and follow the returned preparation and coordinator playbook.
This command only prints instructions from the installed build; it does not start a daemon,
create a team or call a model. Read `piggery skills` as needed for the native Piggery tools.
If the command is missing or fails, report the missing local build instead of inventing a
replacement workflow or installing/restarting Piggery.

An explicit invocation requests the three-arm review, including founding a review team from a
solo session and its three reviewers. Automatic skill discovery alone does not authorize those
actions. A request only to prepare or plan starts no team or reviewers. Preserve optional scope
text as user input, never interpolate it into a shell command.
The short modifier `prepare` means prepare-only; for example, `prepare for commit <SHA>`.

The playbook requires scope/context preparation, exact candidates, route readiness, independent
handbacks and evidence-based adjudication. If already in an unrelated development team, prepare
the scope handoff for a separate solo review session; do not leave or replace that team.
Review authorization does not authorize candidate edits, merges, releases, production execution,
unrelated messages or changes to the Human's decisions.
