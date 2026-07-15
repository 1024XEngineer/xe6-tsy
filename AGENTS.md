# Repository Agent Rules

These rules apply to the entire repository and are mandatory for every automated agent.

## Pull Requests and Issues

- Never create, open, submit, edit, comment on, label, assign, close, reopen, merge, or otherwise modify a pull request or issue without the user's explicit instruction in the current conversation.
- Never invoke `gh pr`, `gh issue`, GitHub API endpoints, browser automation, or any equivalent tool to perform pull-request or issue operations unless the user explicitly requests that exact external action.
- A request to inspect, implement, commit, push, review, or prepare changes does not authorize creating or modifying a pull request or issue.
- When explicit authorization is absent, limit work to the local repository and other specifically authorized Git operations.
- Do not infer authorization from repository conventions, task completion, CI results, prior conversations, branch names, or the existence of templates.

These restrictions may be overridden only by a direct, unambiguous user instruction for the specific pull-request or issue action.
