# Continuum project and quality audit

Reviewed on 23 October 2026 against commit `1ec26fb0b59cb4704ccce28b93fc4046da5a35f4`.

The report contains project context, architecture, implemented workflows, verification results, an implementation roadmap and 120 individually sourced findings. The register separates defects from hardening, policy choices, missing capabilities and polish. Source review is not presented as executed reproduction.

- `main.tex`: report entrypoint; its supporting sources and screenshots are in this folder.
- `BACKLOG.md`: readable issue register.
- `findings.csv`: sortable backlog with source, trigger/consequence, fix and acceptance checks.
- `findings.json`: structured evidence, source lines and SHA-256 hashes.
- `summary.json`: counts by priority, category, kind and evidence level.
- `evidence/verification.json`: check outcomes and explicit verification limits.
- `evidence/go-tests.log`: complete Go check, including the two clock-sensitive authentication failures.
- `evidence/browser-tests.log`: eight passing browser journeys.
- `evidence/focused-probes.json`: synthetic observations from the isolated API.

This is the historical baseline. Follow-up corrections are recorded in the [200-item fix register](../../quality-fixes-2026-10-23.md). Compile the supplied TeX directly to reproduce this PDF after applying those fixes; source/hash validation in the export generator requires the baseline source revision.

At the baseline revision, regenerate the issue exports and compile from the repository root:

```sh
python3 docs/reports/continuum-audit-2026-10-23/build_report.py
mkdir -p output/pdf/continuum-audit-build
cd docs/reports/continuum-audit-2026-10-23
tectonic --outdir ../../../../output/pdf/continuum-audit-build main.tex
```

To rerun the isolated observations from the repository root:

```sh
go run ./docs/reports/continuum-audit-2026-10-23/probes
```

The helper uses `httptest` and synthetic in-memory records. It does not contact AWS or use existing application data. Running it does not fix the observed behaviors; its JSON makes them inspectable.

The LaTeX document can also be opened and compiled as a dedicated project in the Codex desktop editor. Compiling the supplied sources does not require rerunning the generator. Regeneration requires the repository source tree for locator/hash validation.
