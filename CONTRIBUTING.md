# Contributing to Cache-Pot

First off, thank you for being here. Cache-Pot is a young open source project, and that is exactly why your help matters so much right now. Early contributors shape what the project becomes. Whether you fix a typo or add a whole feature, you are making it better, and you will be credited for it.

You do not need to be an expert. You do not need to know Go well. If you are curious and willing to try, you belong here.

## Ways you can help

You can help even without writing code:

- Use Cache-Pot and tell us what felt confusing or broke.
- Improve the README or docs, or add an example.
- Test Cache-Pot with a Redis client in your language (Python, Node, Go, anything) and report what worked and what did not.
- Suggest a feature or share how you would use Cache-Pot.
- Star the repo and share it with friends. Reach matters for a new project.

And of course, code is very welcome too:

- Add a Redis command that is missing.
- Fix a bug.
- Add tests.
- Improve performance.

## Good first issues

Look for issues labeled `good first issue`. These are picked to be small and friendly for newcomers. If there are none open yet, comment on any issue or open a new one and we will help you find a starting point.

## How to set up the project

You need [Go](https://go.dev/dl/) version 1.25 or newer. Then:

```bash
git clone https://github.com/subh05sus/cache-pot
cd Cache-Pot

go run ./cmd/cache-pot     # start the server
go test ./...          # run the tests
```

That is the whole setup. Cache-Pot has no external dependencies, so there is nothing else to install.

## The basic workflow

1. Open an issue first to talk about your idea. This avoids wasted work and keeps the project focused. For tiny fixes (typos, small docs) you can skip straight to a pull request.
2. Fork the repo and create a branch:
   ```bash
   git checkout -b my-change
   ```
3. Make your change.
4. Run these before you push:
   ```bash
   gofmt -w .
   go vet ./...
   go test ./...
   ```
5. Commit and push, then open a pull request. Describe what you changed and why in plain words.

## Code style

- Keep it simple and readable. Match the style of the code around you.
- Add a short comment when the reason for something is not obvious.
- Add or update a test when you change behavior.
- Run `gofmt` so formatting stays consistent.

## Where things live

A quick map of the code is in [docs/architecture.md](docs/architecture.md). In short:

- `cmd/cache-pot` is where the program starts.
- `internal/server` handles connections and every command.
- `internal/store` is the data, all the key value types and expiry.
- `internal/vector` is the vector search.
- `internal/mcp` is the AI agent (MCP) server.
- `internal/dashboard` is the web dashboard.

Adding a new command is usually as easy as writing one handler function and registering its name. Look at the existing `cmd_*.go` files for the pattern.

## Reporting a bug

Open an issue and include:

- What you ran.
- What you expected.
- What actually happened (paste the output if you can).
- Your OS and Go version.

The more detail, the faster we can help.

## Be kind

Please be respectful and patient with each other. We want Cache-Pot to be a friendly place where people feel safe asking questions, including beginner questions. There are no silly questions here.

## License

By contributing, you agree that your contributions are licensed under the project's [BSD-3-Clause](LICENSE) license.

Thanks again. Welcome to Cache-Pot.
