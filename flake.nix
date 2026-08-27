{
  description = "tmux session manager for development environments";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];
      forEachSystem = nixpkgs.lib.genAttrs systems;

      bloomFor = pkgs: pkgs.callPackage ./package.nix { src = self; };
    in
    {
      packages = forEachSystem (system:
        let bloom = bloomFor nixpkgs.legacyPackages.${system};
        in { inherit bloom; default = bloom; });

      overlays.default = final: _prev: { bloom = bloomFor final; };
    };
}
