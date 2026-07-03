# Pre-MVP-508 CRD baseline

These manifests capture the served v1alpha1 schemas immediately before
MVP-508. `scripts/check_yaml.py` verifies that current CRDs remain an additive
upgrade from this baseline. Do not update these fixtures to make a breaking
schema change pass; introduce a new API version and migration path instead.
