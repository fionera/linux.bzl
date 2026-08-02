"""Private module extension providing host-native LLVM Kconfig probe tools."""

load("@llvm//:host_tools_repository.bzl", "llvm_host_tools_repository")

visibility("private")

def _linux_probe_tools_impl(_module_ctx):
    llvm_host_tools_repository(
        name = "linux_bzl_probe_llvm",
        llvm_version = "22.1.8",
    )

linux_probe_tools = module_extension(
    implementation = _linux_probe_tools_impl,
)
