# Kubit — working rules

## Code
- Never write code comments. Names, types and structure carry the meaning; if a
  line needs a comment, rewrite the line.
- Match the surrounding code's idiom, naming and density.

## Product
- Every view and page is live and responsive: state changes from the daemon
  appear on their own, never through a manual page refresh. Anything time-based
  (elapsed, "ago", freshness) ticks from the shared clock signal (`web/src/clock.ts`),
  not from re-renders the user has to trigger.
- Always prefer WebSocket push over polling. The single `/api/v1/ws` stream carries
  typed messages; views only refetch when the daemon sends a `refresh` for their
  scope. Do not add timers that poll the API.
- UI copy is short and professional. The user has to read every word on screen:
  a section help is one sentence, a hint is a fragment, a dialog states the effect
  in one line. No background explanations, no lists of examples, no "why" prose in
  the interface — that belongs in README.md. No ellipses in action labels.

## Process
- The user commits; do not run git commits or pushes.
- No CI / packaging work unless asked ("focus on the product").
- Every task names the files it may change; extra changes go to `NOTES/backlog.md`.
- README.md is the source of truth for status and design; keep it current.
