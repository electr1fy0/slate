# Continuum course project report

Self-contained LaTeX project for **Cloud Architecture Design (BCSE355L), Fall Semester 2026–27**.

Cover metadata is configured near the top of `main.tex`:

- Ayush Pandey, 24BIT0394
- Under the guidance of Venketesh P, Professor Grade 2, SCORE
- Vellore Institute of Technology, Vellore

The cover uses a title in sentence case, the official VIT logo, and no decorative horizontal rules. A4 pages use Latin Modern serif type, black headings and links, numbered chapters, linked contents, figures, tables, references, and a reproducibility appendix. The original colours of the logo and application screenshots are retained.

## Compile

From this directory:

```sh
mkdir -p build
tectonic --keep-logs --outdir build main.tex
```

Alternatively, upload this entire project to Overleaf and select `main.tex`. XeLaTeX is compatible. All figures are local; compiling needs no running app, AWS access, shell escape, or chart-generation step.

## Figures and provenance

The report includes 13 application screenshots, five vector graphs, and three LaTeX architecture/contract diagrams. Screenshots from the teacher, learner, and local recovery workflows were captured from the running application on 23 October 2026, with a synthetic BCSE355L classroom and five-question assessment. `data/local-capture-summary.json` preserves the local fault sequence. Local mode uses separate HTTP servers in one process with shared memory; it does not establish cloud durability. The historical AWS cockpit is separately captioned.

The AWS measurements are from 10 October 2026. `data/cloud-measurements.json` includes only fields used in the analysis, extracted from the repository's cloud rehearsal and independent record/workflow inspection. It excludes credentials and private deployment URLs. `data/cloud-timeline.csv` aligns recorded events to the fault action. The controller timing window's beginning is derived from the recorded end event and duration, not independently timestamped.

Two graphs are analytical models, not measured benchmarks. Their assumptions are in `data/model-parameters.json`; only the one-minute monitoring schedule is implemented. Existing chart PDFs are included. To regenerate all five graphs:

```sh
python -m pip install matplotlib numpy
python figures/build_figures.py
```

`figures/build_figures.py` also produces PNG previews. No fabricated benchmark or cost figures are used.

Logo source: https://vit.ac.in/wp-content/uploads/2023/06/blue_logo-copy-scaled.webp

## Source and validation

- `main.tex`: formatting, cover metadata, front matter
- `sections/`: eight chapters, references, reproducibility appendix
- `images/`: official logo
- `screenshots/`: original application captures used in the report
- `figures/`: vector plots, previews, regeneration script
- `data/`: measurement extracts, aligned CSV, explicit model assumptions

The report describes the supported single-Region deployment in `infra/demo`. Six existing Go suites passed with the race detector on 23 October 2026, as listed in the report. These checks are distinct from the dated cloud rehearsal. The revision adds source-backed analysis of promotion races, current attempt-timing semantics, lost-response replay, and reconciliation work. No cloud resources were changed during report preparation.

The finished PDF is delivered separately at `output/pdf/Continuum_BCSE355L_Report.pdf` from the repository root.
