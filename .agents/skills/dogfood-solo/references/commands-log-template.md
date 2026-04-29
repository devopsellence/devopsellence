# Commands Log

Scenario: {{scenario}}
Target version/commit: {{target_version}}
Validation mode: <local-build | official-artifact | installed-stable | unknown>
Run started: <UTC timestamp>
Run directory: <path>

Keep this file redacted. Never paste raw tokens, secret values, private SSH keys, or plaintext secret outputs.

## Step Template

### <step number>. <short title>

- Time: <UTC timestamp>
- Working directory: `<path>`
- AI-agent intent: <what this command proves or changes>
- Approval state: <not required | approved by user | blocked waiting approval>
- Command:

```sh
<redacted command>
```

- Exit code: `<code>`
- Evidence excerpt:

```text
<minimal redacted output or JSON excerpt>
```

- Interpretation: <what the AI agent can conclude, and what remains uncertain>
- Follow-up: <none | command/test/issue/fix>
