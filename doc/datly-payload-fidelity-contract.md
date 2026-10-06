# Datly payload fidelity contract

The payload reference reader intentionally changes two historical behaviors to
preserve binary and ordered AG-UI content:

- An uncompressed inline body retains every byte, including leading/trailing
  whitespace. The previous reader trimmed text, which changed exact content and
  could change structured/media receipts and replay comparisons.
- A body marked `gzip` must decode successfully. Invalid headers, truncated
  streams and checksum failures return a payload-scoped error. The previous
  fallback treated corrupt compressed bytes as text and trimmed them, hiding
  storage corruption and presenting a false successful payload.

These are approved fidelity changes, not unchanged legacy parity. The raw
storage route keeps its existing field/selector mapping. The binary reference
route is the decoding boundary; its failure must not mutate stored bytes.

`TestPayloadFidelityAndWriterContracts` retains writer defaults, threshold
compression, sparse mutation, nullable/omitted values, required fields,
tenant predicates and transactional rollback assertions. Its whitespace
expectation is exact, and corrupt gzip has a separate explicit error test.
`TestPayloadSelectorProxyContracts` retains ordering, limits, offsets, selected
fields, trusted bound criteria, tenant isolation, snapshot immutability and
invocation-local selector reuse. Its healthy size-15 fixture avoids making
selector behavior depend on corrupt-payload decoding. Corruption is inserted
only by its dedicated fixture, which also verifies unchanged stored bytes and
that a healthy selected payload remains readable.

No reader/writer implementation or generated contract was changed by this test
update. The payload reader implementation and byte-safe message bridge were
updated and verified separately.

Verification: the complete `internal/datly/contracttest` suite passed with
`-count=1 -v` (108.54 seconds test execution; 115.79 seconds wall time).
Verbose output and exit status are saved at
`/tmp/agui-contracttest-logs/full-contracttest-20261002.log`.
The focused payload suite also passed; its log is
`/tmp/agui-contracttest-logs/payload-fidelity-focused.log`.
