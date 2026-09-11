# Release publisher

`go run ./scripts/release` creates a signed, portable bundle for offline
import. The Ed25519 private key remains an operator-held file and is never
read by the control server.

```sh
go run ./scripts/release \
  --artifact apps/agent/dist/scout-agent-linux-amd64 \
  --private-key ./publisher-ed25519.pkcs8 \
  --key-id 0123456789abcdef \
  --version 0.2.0 --generation 2 \
  --output ./release-bundles/scout-agent-0.2.0.json
```

Add the corresponding public key to the server's `SCOUT_RELEASE_TRUST_FILE`
as `key-id base64-public-key`. Trust changes are explicit and should be
audited. Test keys belong only in disposable fixtures.
