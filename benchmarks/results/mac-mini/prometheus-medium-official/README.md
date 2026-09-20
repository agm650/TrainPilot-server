# Mac mini official medium Prometheus evidence

This directory contains the Prometheus evidence for the three official
`medium` Mac mini runs performed on 2026-09-20.

Each run directory contains gzip-compressed Prometheus HTTP API matrix results
at the original two-second scrape resolution. The retained interval covers
setup, warm-up, and measurement. `manifest.json` records the exact selectors,
boundaries, run IDs, series counts, and sample counts.

The uncompressed `system-metrics.json` files are the versioned summaries used
by `trainpilot-bench enrich-report`. The query responses used to derive them
are retained beside each summary. CPU p95 remains in `derived-summary.json`
because system-metrics schema version 1 stores CPU maximum rather than CPU p95.

The hardware and server metadata are stored in
`../mac-mini-run-metadata.json`. The three enriched reports in the parent
directory pass `validate-report --publication`.

All thermal evidence is retained in the node exports. The
`node_cpu_core_throttles_total` counters remained zero for both cores in every
run, and no critical thermal alarm was observed.

Validate the archive from this directory with:

```sh
shasum -a 256 -c SHA256SUMS
gzip -t run-*/*.json.gz
jq -e '.status == "success"' prometheus-*.json
jq -e '.' manifest.json derived-summary.json run-*/system-metrics.json
```

The raw Prometheus time series are evidence artifacts and should not be added
to a published Git baseline. Store them in controlled external storage and
retain the checksums and immutable archive location in the published summary.
