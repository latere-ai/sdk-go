# API documents

The Latere API's OpenAPI documents for the capabilities this SDK covers,
converted to JSON with sorted keys. The tests hold the models to them.
`make openapi` refreshes them from the public API reference, and
`sources.json` records where each came from and the SHA-256 of what was
fetched. Do not edit them by hand.

To check the models against a change to a document before it is
published, convert it from a local file, such as the core's branch:

```sh
cd tools && go run ./openapi -dir ../openapi -local agents=path/to/openapi.yaml
```

`sources.json` then records the SHA-256 of that file; the next
`make openapi` fetches the published document again.
