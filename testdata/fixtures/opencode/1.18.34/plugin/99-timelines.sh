#!/usr/bin/env bash
# Regenerates captures/timelines.txt: the compact per-scenario views the README points at (n = line of the capture file).
cd "$(dirname "$0")"
sec() { echo; echo "##### $1  [$2 lines $3-$4]"; ./timeline.sh "captures/$2" "$3" "$4"; }
{
echo "# columns: n, ms since the capture's first line, kind (event|hook|tool|ctl|client), name, session (last 6), detail"
sec "Q2 plain turn" tui-1-port-turns-steer.jsonl 60 171
sec "Q2 turn with tool calls (bash + plugin tool)" tui-1-port-turns-steer.jsonl 172 284
sec "Q2 steer: a prompt typed while busy (TUI)" tui-1-port-turns-steer.jsonl 285 391
sec "Q5 wake: promptAsync into an idle session" tui-2-port-wake-abort.jsonl 60 169
sec "Q5 prompt noReply into an idle session" tui-2-port-wake-abort.jsonl 170 175
sec "Q7 abort: Esc Esc in the TUI, steer message pending" tui-2-port-wake-abort.jsonl 176 251
sec "Q7 abort: POST /session/:id/abort while busy, then again while idle" tui-2-port-wake-abort.jsonl 252 342
sec "Q7 the next prompt after an abort" tui-2-port-wake-abort.jsonl 343 423
sec "Q8 permission edit=ask" tui-3-port-permission-model-sessions.jsonl 60 201
sec "Q9 model glm-b + variant low" tui-3-port-permission-model-sessions.jsonl 203 251
sec "Q6 /new: second root session" tui-3-port-permission-model-sessions.jsonl 252 329
sec "Q6 served second session (POST /session + prompt_async)" tui-3-port-permission-model-sessions.jsonl 330 386
sec "Q6 task tool: child session" tui-3-port-permission-model-sessions.jsonl 387 563
sec "Q6 another directory: a third plugin instance" tui-3-port-permission-model-sessions.jsonl 564 569
sec "Q2/Q5 steer through the plugin client, --port TUI (the command ran twice: see README)" tui-3-port-permission-model-sessions.jsonl 570 698
sec "Q2/Q5 steer through the plugin client, no-port TUI" tui-4-noport-config-content.jsonl 55 219
sec "Q5 wake, no-port TUI" tui-4-noport-config-content.jsonl 220 284
sec "Q5 noReply, no-port TUI" tui-4-noport-config-content.jsonl 285 291
} >captures/timelines.txt
