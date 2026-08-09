# sing-box-wrap

Ackwrap maintains its sing-box core as a reproducible patch stack instead of a
source fork with merged upstream history.

The `sing-box/` submodule points to an exact official SagerNet/sing-box commit.
Ackwrap production changes live in `patches/` and are applied in the order
listed by `patches/series`.

Prepare a disposable patched checkout:

```bash
python scripts/prepare_core.py
```

Build the prepared core:

```bash
cd .work/sing-box
make build
go build ./cmd/sing-box
```

Do not edit the official submodule directly. Update its gitlink and
`patches/upstream.txt` together, then verify that every patch still applies and
the prepared source builds before accepting an upstream update.

The initial patch stack intentionally contains production code only. Ackwrap
test additions will be migrated separately after the production stack is
stable.
