{
  description = "agent-sessions collector and development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  # Official Rust binaries avoid the locked nixpkgs Rust/LLVM profiler mismatch.
  inputs.rust-overlay = {
    url = "github:oxalica/rust-overlay/d3fe74269f2a6652437e21e78eb7e83be81f6982";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    self,
    nixpkgs,
    rust-overlay,
    ...
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin"];
    forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    # A build from uncommitted changes says so, as goreleaser's would.
    version = self.shortRev or self.dirtyShortRev or "dev";
    # The pinned official toolchain and the coverage tool built with it.
    rustParts = pkgs: let
        rustPkgs = pkgs.extend (import rust-overlay);
        rustToolchain = rustPkgs.rust-bin.stable."1.99.0".minimal.override {
          extensions = ["clippy" "rustfmt" "llvm-tools-preview"];
        };
        llvmToolDir = "${rustToolchain}/lib/rustlib/${pkgs.stdenv.hostPlatform.rust.rustcTarget}/bin";
        coverageTool = (pkgs.cargo-llvm-cov.override {
          rustPlatform = pkgs.makeRustPlatform {
            cargo = rustToolchain;
            rustc = rustToolchain;
          };
        }).overrideAttrs (old: {
          # Keep upstream tests enabled, using this compiler's own profile format.
          env = old.env // {
            LLVM_COV = "${llvmToolDir}/llvm-cov";
            LLVM_PROFDATA = "${llvmToolDir}/llvm-profdata";
            CARGO_BUILD_JOBS = "2";
            RUST_TEST_THREADS = "2";
          };
        });
    in {inherit rustToolchain llvmToolDir coverageTool;};
  in {
    packages = forAllSystems (pkgs: {
      # The overridden coverage tool is never in cache.nixos.org; CI caches its build.
      cargo-llvm-cov = (rustParts pkgs).coverageTool;
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

    devShells = forAllSystems (pkgs: let
      inherit (rustParts pkgs) rustToolchain llvmToolDir coverageTool;
      # Shared by the default and rust shells so the two cannot drift.
      rustTools = [
        # Official toolchain source and tool versions are locked independently.
        rustToolchain
        pkgs.cargo-nextest
        pkgs.cargo-deny
        coverageTool
      ];
      # Tools the Rust gate scripts invoke: bash, mkfifo (coreutils), python3.
      # The verification helper also uses compression.zstd and sqlite3 (python).
      gateShellTools = [pkgs.git pkgs.bash pkgs.coreutils pkgs.gnugrep];
      rustEnv = {
        CARGO_BUILD_JOBS = "2";
        RUST_TEST_THREADS = "2";
        # Use llvm tools from the same locked package set, not rustup downloads.
        LLVM_COV = "${llvmToolDir}/llvm-cov";
        LLVM_PROFDATA = "${llvmToolDir}/llvm-profdata";
      };
    in {
      # Rust gates only: 148 store paths against 205 in the default shell.
      rust = pkgs.mkShell ({
          packages = rustTools ++ gateShellTools ++ [pkgs.python314];
        }
        // rustEnv);
      # Explicit alias retained for coverage diagnostics.
      coverage = pkgs.mkShell {
        inputsFrom = [self.devShells.${pkgs.stdenv.hostPlatform.system}.default];
        packages = [];
      };
      # mkShell includes a C compiler: the race detector needs cgo even though
      # release builds and the collector's dependencies are pure Go.
      default = assert pkgs.lib.assertMsg
      ("v${pkgs.golangci-lint.version}" == pkgs.lib.trim (builtins.readFile ./.golangci-lint-version))
      "Update the nixpkgs lock to match .golangci-lint-version";
        pkgs.mkShell ({
            packages =
              rustTools
              ++ [
                pkgs.cargo-mutants
                pkgs.go_1_27
                pkgs.golangci-lint
                pkgs.gnumake
              ]
              ++ gateShellTools
              ++ [
                pkgs.curl
                pkgs.jq
                # The verification helper uses compression.zstd and sqlite3.
                pkgs.python314
                # Analytics fixture tests invoke the CLI, not Python compression.zstd.
                pkgs.zstd
                # Generated producer-handler tests strip types and mock spawn; no SDK is run.
                pkgs.nodejs_24
                pkgs.shellcheck
                pkgs.zizmor
                pkgs.goreleaser
                pkgs.syft
              ];

            # Use the locked compiler, not an implicitly downloaded toolchain.
            GOTOOLCHAIN = "local";
          }
          // rustEnv);
    });

    formatter = forAllSystems (pkgs: pkgs.alejandra);
  };
}
