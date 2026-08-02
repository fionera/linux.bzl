"""Release metadata for prebuilt kconfig repository-rule tools.

Each archive must extract the requested host executables at its root.
"""

visibility("//...")

KCONFIG_TOOL_VERSION = "v0.0.24-adaptive"

_RELEASE_BASE_URL = "https://github.com/fionera/linux.bzl/releases/download/kconfig-{version}".format(
    version = KCONFIG_TOOL_VERSION,
)

KCONFIG_TOOL_RELEASES = {
    "darwin_amd64": struct(
        integrity = "sha256-4li5J+q7uAbQQMg0g7htIVZEMUVs3w5WNDAKwNu3FWo=",
        urls = ["{}/kconfig-darwin-amd64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
    "darwin_arm64": struct(
        integrity = "sha256-0c4trAW+BzQCUiczKhu39UpjKurerVPZfzD15MtzDB8=",
        urls = ["{}/kconfig-darwin-arm64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
    "linux_amd64": struct(
        integrity = "sha256-NnF/kA6n9KItTYIEV0foxi4vzeFZ+yzZsklQoIbI5iA=",
        urls = ["{}/kconfig-linux-amd64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
    "linux_arm64": struct(
        integrity = "sha256-9y4T6Pr49ve/I0+lQ5HIEYl0g5caWsdmAkMM6OOkhK0=",
        urls = ["{}/kconfig-linux-arm64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
    "windows_amd64": struct(
        integrity = "sha256-nnaG6uRH8zwoDbV8WMn4Z65rmexB6uESeaQ6dTazRCE=",
        urls = ["{}/kconfig-windows-amd64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
    "windows_arm64": struct(
        integrity = "sha256-AepyL0D3OHH2iSBtVNmdLqtt17BBqiW1Nmmh/LZA1Bg=",
        urls = ["{}/kconfig-windows-arm64.tar.zst".format(_RELEASE_BASE_URL)],
    ),
}
