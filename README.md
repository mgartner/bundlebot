# bundlebot

A command line program for analyzing CockroachDB statement bundles built on (and
with) OpenAI.

# Usage

1. Clone the repository.
2. Build the binary: `go build .`.
3. Set the environment variable `OPENAI_API_KEY` to your OpenAI API key.
4. Run the bot providing a path to a statement bundle: `./bundlebot stmt-bundle-1234.zip`.
