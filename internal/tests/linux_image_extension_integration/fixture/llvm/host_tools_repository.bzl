"""Small host-native LLVM tools repository fixture."""

def _llvm_host_tools_repository_impl(rctx):
    if not rctx.os.name or not rctx.os.arch:
        fail("repository host platform is unavailable")
    rctx.file("clang.exe", "#!/bin/sh\nexit 0\n", executable = True)
    rctx.file("ld.lld.exe", "#!/bin/sh\nexit 0\n", executable = True)
    rctx.file(
        "host-platform.txt",
        "%s/%s\n" % (rctx.os.name, rctx.os.arch),
        executable = False,
    )
    rctx.file("llvm-host-tools.json", "{}\n", executable = False)
    rctx.file("resource-dir.txt", "resource\n", executable = False)
    rctx.file(
        "BUILD.bazel",
        "exports_files([\"clang.exe\", \"ld.lld.exe\", \"host-platform.txt\", \"llvm-host-tools.json\", \"resource-dir.txt\"])\n",
        executable = False,
    )
    return rctx.repo_metadata(reproducible = False)

llvm_host_tools_repository = repository_rule(
    implementation = _llvm_host_tools_repository_impl,
    attrs = {"llvm_version": attr.string(mandatory = True)},
)
