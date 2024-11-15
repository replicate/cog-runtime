Asynco de Coggo
===============

Minimalistic rewrite of Cog, the fun parts.

* Python
  * Zero dependency
  * Single file data class only API surface
  * Async by default, single process runner
  * File based input & output
  * POSIX signal IPC with HTTP parent
* Go
  * HTTP server
  * Logging

TODO:
* Python
  * Test all Python versions
* Go
  * Output file encoding and upload
  * Webhook interval
  * E2E tests
* Build
  * Go binary
  * Python source tree
  * Install both in monobase build.sh
