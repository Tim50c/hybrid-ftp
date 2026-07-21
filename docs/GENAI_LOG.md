# GenAI Usage & Code Refinement Log

> **Mandatory Appendix per Section 4.3 of the project specification.**
>
> Every significant use of Generative AI during the development of this project MUST be logged here.
> Include exact prompts, raw AI output, and documented refinements applied before integration.

---

## Log Entry Template

Each entry must contain:

```markdown
### Entry #N — [Brief Title]

**Date**: YYYY-MM-DD
**Model/Tool**: [e.g., ChatGPT-4, Claude 3.5 Sonnet, GitHub Copilot, etc.]
**Developer**: [Developer A / Developer B]
**Phase**: [Phase 1-5 from TASKS.md]
**Module**: [e.g., internal/rdt/bbr.go, cmd/server/main.go]

#### Prompt
```
[Exact prompt text provided to the AI]
```

#### Raw AI Output
```
[Verbatim output as received from the AI — code, explanation, or both]
```

#### Refinements & Debugging Applied
1. [Specific change 1: what was wrong and how it was fixed]
2. [Specific change 2: ...]
3. ...

#### Final State
- **Commit SHA**: [git commit hash where the refined code was committed]
- **File(s) modified**: [list of files]
- **Verification**: [test results, manual verification notes]
```

---

## Log Entries

### Entry #1 — Project Architecture & Documentation Generation

**Date**: 2026-07-21
**Model/Tool**: Claude (via opencode)
**Developer**: Both
**Phase**: Phase 1 — Project Bootstrap
**Module**: `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/TASKS.md`, `docs/GENAI_LOG.md`

#### Prompt
```
[See ROLE & MISSION prompt at top of this session — full architectural interview and documentation generation request]
```

#### Raw AI Output
```
[The architecture decisions, header design, BBR specification, task breakdown, and all four documents 
as generated in this session. Full conversation transcript serves as the raw output reference.]
```

#### Refinements & Debugging Applied
1. Removed SACK bitmask from RDT header (per developer feedback — Selective Repeat with per-packet timers does not require SACK/NACK)
2. Reduced Flags field from 2 bytes to 1 byte (5 flags used, 3 bits reserved)
3. Added Session ID field (4 bytes, uint32) to enable single-server-socket demultiplexing for multiple concurrent clients
4. Changed congestion control from TCP Tahoe AIMD to BBR v1 (per developer preference)
5. Set MODE B/C handling to return `504 Not implemented.` with real implementation deferred
6. Confirmed SHA-256 (not MD5) for HASH command
7. Confirmed STOU filename format as `UUID_timestamp_originalname`

#### Final State
- **Commit SHA**: (initial commit — TBD)
- **File(s) modified**: `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/TASKS.md`, `docs/GENAI_LOG.md`, `go.mod`
- **Verification**: Documents reviewed and approved by both developers

---

### Entry #2 — [Reserved for Next AI-Assisted Session]

**Date**: YYYY-MM-DD
**Model/Tool**: 
**Developer**: 
**Phase**: 
**Module**: 

#### Prompt
```
[TBD]
```

#### Raw AI Output
```
[TBD]
```

#### Refinements & Debugging Applied
[TBD]

#### Final State
- **Commit SHA**: 
- **File(s) modified**: 
- **Verification**: 

---

### Entry #3 — [Reserved]

...

---

## Summary of AI Usage

| Entry | Date | Module | AI Used For | Refinements Needed |
|-------|------|--------|-------------|-------------------|
| 1 | 2026-07-21 | All docs | Architecture design, task planning, documentation generation | 6 refinements (see above) |
| 2+ | TBD | TBD | TBD | TBD |

---

## Notes

- All AI-generated code MUST undergo human review and testing before commit.
- Any code the developer cannot explain step-by-step in the oral viva will result in a fail for the Theoretical Understanding criteria (per Section 5 of requirements).
- This log, combined with the git history, serves as the complete GenAI provenance record required by the grading rubric.
