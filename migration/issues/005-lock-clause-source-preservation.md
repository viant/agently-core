# Preserve conditional locking clauses during transcription

Status: open, required before tree deletion can claim locking parity.

The authored canonical reader contains `#if($LockRows) ${View.ForUpdate()} #end` inside its named source. Datly `transcribe/compile/reader.go` calls `lineDirectiveSkeleton`, which removes an entire line beginning with `#if`, `#else`, or `#end`, including inline SQL/template content. The parsed named source and its generated SQL therefore lose the locking clause.

The runtime API itself rejects a lock read without an active transaction and renders the expected dialect clause. Its direct runtime and builder tests pass, but these tests do not prove that operation transcription retains the clause. The linked-host deletion-plan lock boundary test fails against current generated artifacts and remains intact.

The fix must preserve original executable named sources while using a parser-backed analysis skeleton. It must cover root and child sources, conditionals and original predicate bindings, successful regeneration, lock rejection outside a transaction, real managed invocation behavior, and actual emitted MySQL SQL. Direct generated edits or omitting the assertion do not satisfy this gate.

The tree and schedule components are authored and use serializable managed units. Functional schedule deletion cases pass; MySQL lock/contention and root/application gates remain unverified.
