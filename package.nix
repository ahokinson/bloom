{ buildGoModule, src }:

let
  version = "0.2.1";
in
buildGoModule {
  pname = "bloom";
  inherit version src;

  vendorHash = "sha256-g+yaVIx4jxpAQ/+WrGKxhVeliYx7nLQe/zsGpxV4Fn4=";

  subPackages = [ "cmd/bloom" ];

  # main.version defaults to "dev"; without this `bloom --version` reports
  # that instead of what was built.
  ldflags = [ "-X main.version=${version}" ];

  meta = {
    description = "tmux session manager for development environments";
    homepage = "https://github.com/ahokinson/bloom";
    mainProgram = "bloom";
  };
}
