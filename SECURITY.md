# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |

## Reporting a Vulnerability

We take the security of gokit seriously. If you discover a security vulnerability, please follow these steps:

### How to Report

**DO NOT** open a public GitHub issue for security vulnerabilities.

Instead, please report security vulnerabilities by emailing:

**security@dhawalhost.com** (or your preferred security contact email)

### What to Include

Please include the following information in your report:

- Description of the vulnerability
- Steps to reproduce the issue
- Potential impact of the vulnerability
- Suggested fix (if you have one)
- Your contact information for follow-up

### Response Timeline

- **Initial Response**: We will acknowledge receipt of your vulnerability report within 48 hours.
- **Status Update**: We will provide a detailed response indicating the next steps within 7 days.
- **Fix Timeline**: We aim to release a fix for confirmed vulnerabilities within 30 days, depending on complexity.

### Disclosure Policy

- We follow a coordinated disclosure approach
- We will credit researchers who report valid vulnerabilities (unless anonymity is preferred)
- Please allow us adequate time to address the issue before public disclosure
- We will publicly acknowledge the fix in our CHANGELOG and release notes

### Security Best Practices for Users

When using gokit in production:

1. **Keep Dependencies Updated**: Regularly update to the latest version
2. **Validate Configuration**: Ensure all security-related configuration is properly set
3. **Use Strong Secrets**: Use cryptographically secure secrets for JWT, API keys, and encryption
4. **Enable TLS**: Always use TLS in production environments
5. **Monitor Logs**: Implement proper logging and monitoring
6. **Rate Limiting**: Configure appropriate rate limits for your use case
7. **Health Checks**: Ensure health check endpoints are properly secured

### Known Security Considerations

- **JWT Secrets**: Ensure JWT secrets are at least 32 bytes and cryptographically secure
- **AES Keys**: AES-256-GCM requires exactly 32-byte keys
- **Database Credentials**: Never commit database credentials to version control
- **API Keys**: Store API keys in secure secret management systems
- **CORS Configuration**: Avoid using `CORSAllowAll()` in production

### Security Features

gokit includes the following security features:

- AES-256-GCM encryption
- JWT HS256/RS256 signing and verification
- bcrypt password hashing
- Rate limiting (in-memory and Redis-backed)
- Circuit breaker for fault tolerance
- Secure HTTP headers middleware
- CORS middleware with configurable policies
- Request ID tracking
- Recovery middleware for panic handling

Thank you for helping keep gokit secure!
