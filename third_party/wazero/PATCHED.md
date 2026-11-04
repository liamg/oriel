# wazero, patched

This is wazero v1.12.0 (github.com/tetratelabs/wazero), the packages Oriel uses, with one change: the file
compilation cache (`internal/filecache/file_cache.go`) hands the engine a buffered reader. The engine decodes a cached
module eight bytes at a time, and an unbuffered file made each of those a system call: a cached grammar's open cost
15 to 22 ms on CI's VMs, mostly in syscalls.

It also makes the cache's folder again before adding an entry, when it's gone since the cache was set up (deleted
while the program runs): wazero fails the compile when it can't keep its result, and a grammar then never parses.

`go.mod` replaces the module with this directory. Drop it once a wazero release buffers the read.
