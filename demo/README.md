# demo/

Tape file for [`vhs`](https://github.com/charmbracelet/vhs) used to render the README hero.

## Render

Install `vhs` and ensure the `testagent` binary is on `PATH` (or run `go install .` from the repo root).

```sh
vhs demo/hero.tape
```

Outputs `demo/hero.gif`, used as the README hero. Re-render whenever the tape or the binary's output changes.
