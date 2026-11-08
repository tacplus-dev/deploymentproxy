# The gates the build pipeline runs (`just gates`, in the image build.lock pins) and a land runs: make check, without
# Docker. `make image` (Docker) is not a gate there; the release workflow builds the image.
gates:
    make check
