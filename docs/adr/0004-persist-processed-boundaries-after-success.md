# Persist processed boundaries after success

A Reset Boundary is persisted as processed only after the Codex streaming inference completes successfully. We accept a rare duplicate if the process crashes between upstream success and the atomic state write; persisting a claim before the request was rejected because it could silently lose the activation after a crash or failed request.
