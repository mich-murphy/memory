{
  description = "memory session capture (Rust) development environment";

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
      # Rust gates only (CI uses this shell).
      rust = pkgs.mkShell ({
          packages = rustTools ++ gateShellTools ++ [pkgs.python314];
        }
        // rustEnv);
      # The local shell: the rust shell plus cargo-mutants (rust/README.md).
      default = pkgs.mkShell ({
          packages = rustTools ++ gateShellTools ++ [pkgs.python314 pkgs.cargo-mutants];
        }
        // rustEnv);
      # Explicit alias retained for coverage diagnostics.
      coverage = pkgs.mkShell {
        inputsFrom = [self.devShells.${pkgs.stdenv.hostPlatform.system}.default];
        packages = [];
      };
    });

    formatter = forAllSystems (pkgs: pkgs.alejandra);
  };
}
