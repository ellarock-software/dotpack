# Security Policy

## Reporting a Vulnerability

Please report suspected security vulnerabilities privately by emailing
security@ellarock.software. Include a clear description, reproduction steps, affected
versions or commits, and any known impact.

Do not open a public GitHub issue for security-sensitive reports. We will
acknowledge reports as soon as practical, investigate, and coordinate a fix and
disclosure timeline based on severity.

## Supported Versions

Until the first stable release, security fixes target the default branch. After
versioned releases begin, this file will be updated with the supported release
policy.

## Secret Handling

dotpack installs and merges local agent configuration files. Avoid committing
real API keys, bearer tokens, credentials, or private host paths in `.agents`,
`.claude`, `.gemini`, `.codex`, `.antigravity`, or generated fixtures. Prefer
environment variable references such as `${TOKEN_NAME}` when examples need to
show credential wiring.
