# Where this comes from

Original work, written from scratch on personal time and personal equipment, with no employer code,
assets, data or configuration in it. The scenarios in `examples/` point at localhost.

The one dependency is `gopkg.in/yaml.v3` (Apache-2.0 / MIT, the go-yaml project), used to read the
scenario files. Nothing else is vendored or copied.

The design decisions in this repository come from running game backends: the histogram and the
nearest-rank percentiles are the reporting a 30 Hz server needs, and the exit code is what makes the
number a gate rather than a slide.
