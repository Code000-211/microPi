# Example: plugins compiled into µPi

- `plugins.ts`: the manifest. Its default export lists the extensions to compile in.
- `extensions/word-count.ts`: an ordinary Pi extension, with a `word_count` tool and a `/words` command.

```bash
scripts/build-pi.sh --plugins examples/plugins/plugins.ts --out out/pi-bolt-plugins
out/pi-bolt-plugins/pi          # then: /words README.md
```

The extension works unchanged when loaded at run time (copy it to `~/.pi/agent/extensions/`), which makes it easy to compare the
two. The full guide is [docs/PLUGINS.md](../../docs/PLUGINS.md).
