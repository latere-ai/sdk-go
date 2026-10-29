# Interoperability test vectors

These are the test vectors of the IETF MLS working group, from
https://github.com/mlswg/mls-implementations at commit
`2034fe72e0c805a683ba76165660c834f2a8142a`, in its `test-vectors/`
directory. The formats and the checks each one asks for are in that
repository's `test-vectors.md`.

Each file here is the upstream JSON with the entries of other cipher
suites left out, keeping suites 1, 2, 5 and 7, which this package
implements, re-encoded without whitespace and compressed with gzip. The
files without a cipher suite field are whole. To check one against the
upstream file:

```sh
jq -c '[.[] | select(.cipher_suite == 1 or .cipher_suite == 2 or .cipher_suite == 5 or .cipher_suite == 7)]' treekem.json
```
