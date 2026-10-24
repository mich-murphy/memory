{
  description = "agent-sessions collector and development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = {
    self,
    nixpkgs,
    ...
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    # A build from uncommitted changes says so, as goreleaser's would.
    version = self.shortRev or self.dirtyShortRev or "dev";
  in {
    packages = forAllSystems (pkgs: {
      default = pkgs.buildGo127Module {
        pname = "agent-sessions";
        inherit version;
        # Only the Go module: a docs change doesn't rebuild the binary.
        src = pkgs.lib.fileset.toSource {
          root = ./.;
          fileset = pkgs.lib.fileset.unions [./go.mod ./go.sum ./cmd ./internal];
        };
        # Update after changing go.mod or go.sum: set lib.fakeHash, build, and copy the reported hash.
        vendorHash = "sha256-ngoOGVEr4tjlXVZyOY53k0jffp+FvYfkJkRTcuVmPtM=";
        subPackages = ["cmd/agent-sessions"];
        env.CGO_ENABLED = 0;
        ldflags = ["-s" "-w" "-X main.version=${version}"];
        # `make check` is the gate; the suite spawns agents and runs for minutes.
        doCheck = false;
        meta = {
          description = "Collects Claude Code, Codex and Pi sessions into an R2 bucket";
          mainProgram = "agent-sessions";
          license = pkgs.lib.licenses.mit;
        };
      };
    });

    devShells = forAllSystems (pkgs: {
      # mkShell includes a C compiler: the race detector needs cgo even though
      # release builds and the collector's dependencies are pure Go.
      default = assert pkgs.lib.assertMsg
      ("v${pkgs.golangci-lint.version}" == pkgs.lib.trim (builtins.readFile ./.golangci-lint-version))
      "Update the nixpkgs lock to match .golangci-lint-version";
        pkgs.mkShell {
          packages = [
            pkgs.go_1_27
            pkgs.golangci-lint
            pkgs.gnumake
            pkgs.git
            pkgs.bash
            pkgs.coreutils
            pkgs.gnugrep
            pkgs.curl
            pkgs.jq
            # The verification helper uses compression.zstd and sqlite3.
            pkgs.python314
            # Analytics fixture tests invoke the CLI, not Python compression.zstd.
            pkgs.zstd
            pkgs.shellcheck
            pkgs.zizmor
            pkgs.goreleaser
            pkgs.syft
          ];

          # Use the locked compiler, not an implicitly downloaded toolchain.
          GOTOOLCHAIN = "local";
        };
    });

    formatter = forAllSystems (pkgs: pkgs.alejandra);
  };
}
