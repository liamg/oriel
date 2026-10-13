# Recorded runner output

Each `<name>.rec` is a real run on a fixture under `testdata/<runner>/`: a `#oriel-record` header (the command, its
environment, the tools' versions, the exit status) and then the runner's stdout. `<name>.cover` is the coverage file a
coverage run wrote, and `<name>.events` what the adapter's `Parse` makes of the recording. Machine paths are replaced:
`$ROOT` (the fixture), `$RUNDIR`, `$TOOLS`, `$GOROOT`, `$HOME`.

Recorded with go 1.26.0, Python 3.11.2 with pytest 8.3.4, pytest-cov 6.0.0 and coverage 7.6.10, Node 22.12.0 with
vitest 2.1.8 (and @vitest/coverage-v8 2.1.8) and jest 29.7.0, and Rust 1.83.0 with cargo-nextest 0.9.87 and
cargo-llvm-cov 0.6.15. The headers say so per file.

To record again, put those tools first on PATH and run, from the repo root:

    ORIEL_RECORD_TOOLS=<where the tools are> ORIEL_RECORD_PYTHON3=<python with pytest> \
      go test ./testrun -run 'TestRecord$' -record
    go test ./testrun -run TestRecordedEvents -update

The JS fixtures get their `node_modules` with `npm install` on the first recording. Review the diff of the `.events`
goldens: a change there is a change in what the IDE shows.
