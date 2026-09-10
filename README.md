# zot-question

A Go zot extension that ports the core feature and interaction model of
`npm:@mrclrchtr/supi-ask-user` to zot: an LLM-callable `ask_user` tool that
opens a blocking, keyboard-driven decision form.

## Current proposal / implementation

- One focused form containing 1–10 related questions.
- Single-choice, multi-choice, and free-text questions.
- Recommendations are preselected or prefilled and remain editable.
- Choice descriptions/details are shown beside the focused option.
- Form, question, and option comments.
- A review screen before submission.
- Unanswered questions produce a `needs_discussion` result.
- Escape or closing the panel cancels the tool call.
- No network requests, external process, or persistent answer storage.

The panel uses zot's native extension panel API, so it works without a second
TUI dependency. This is intentionally the first slice: unlike Pi's richer
custom component API, zot panels currently provide lines plus key events. The
interaction is therefore equivalent in behavior, while rendering remains
textual.

## Build and try

```sh
git clone ... ../zot-question
cd ../zot-question
go build -o zot-question .
zot --ext .
```

Or install it:

```sh
zot ext install .
```

The model can then call `ask_user` with a payload such as:

```json
{
  "title": "Project setup",
  "intro": "Choose the defaults for this repository.",
  "questions": [
    {
      "id": "package-manager",
      "type": "choice",
      "header": "Packages",
      "prompt": "Which package manager should be used?",
      "recommendation": "bun",
      "options": [
        {"value":"bun", "label":"Bun", "description":"Fast local installs"},
        {"value":"npm", "label":"npm", "description":"Broadest compatibility"}
      ]
    },
    {
      "id": "notes",
      "type": "text",
      "header": "Notes",
      "prompt": "What should the agent keep in mind?",
      "placeholder": "Optional context",
      "recommendation": "Prefer small, reviewable changes."
    }
  ]
}
```

## Controls

- `↑`/`↓`: move through options or review rows
- `Space`: select/toggle an option
- `Enter`: accept the current answer, or submit from review
- `Tab`: advance to the next question
- `c`: comment on the current question
- `n`: comment on the focused choice
- `u`: mark the question unanswered
- `e`: edit the focused review row; `Esc`: cancel

## Compatibility notes

The package intentionally keeps the same public question vocabulary and limits
as Supi Ask User. The model-visible result is a concise text summary, while
zot's tool result remains the source of truth for the current turn. A later
version can add richer structured result metadata when zot exposes a details
field for extension tool results.
