# demo/

Tape files for [`vhs`](https://github.com/charmbracelet/vhs) used to render the README demos — one per emulated vendor.

## Render

Install `vhs` and ensure the `testagent` binary is on `PATH` (or run `go install .` from the repo root).

```sh
vhs demo/claude.tape   # outputs demo/claude.gif
vhs demo/codex.tape    # outputs demo/codex.gif
```

Both GIFs are embedded at the top of the root README. Re-render whenever the tape or the binary's output changes.
