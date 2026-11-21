#!/usr/bin/env python3
"""Fail when manifest.json's config schema is not a valid JSON Schema 2020-12.

navibeat-mixes#21: 0.9.32 shipped with a description sentence pasted into
mixStyle's "type", and Navidrome refused to load the plugin ("invalid config
schema ... got string, want array"). Navidrome validates the schema against the
2020-12 metaschema when it loads the plugin, so this runs the same check before
a tag is made.
"""
import json
import sys

try:
    import jsonschema
except ImportError:
    sys.exit("check-manifest-schema: pip install jsonschema")

schema = json.load(open("manifest.json"))["config"]["schema"]
try:
    jsonschema.Draft202012Validator.check_schema(schema)
except jsonschema.SchemaError as e:
    sys.exit(f"manifest.json config schema is invalid: {e.message} at {'/'.join(map(str, e.absolute_path))}")
print("manifest config schema valid (JSON Schema 2020-12)")
