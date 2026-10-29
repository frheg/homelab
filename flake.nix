{
  description = "homelab: images for the services in services/";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      pkgs = nixpkgs.legacyPackages.x86_64-linux;

      # One Go service per directory in services/. Add a name here to get
      # .#<name> (binary) and .#image-<name> (container image).
      goServices = { gpuctl = "sha256-44Fhzb370I/fDKT3NgE5QwmXvOSyYoLeHCq5Y3xD1XM="; gpuproxy = null; };

      bin = name: vendorHash: pkgs.buildGoModule {
        pname = name;
        version = "0";
        src = ./services/${name};
        inherit vendorHash;
        env.CGO_ENABLED = "0";
        ldflags = [ "-s" "-w" ];
      };

      image = name: pkg: pkgs.dockerTools.buildLayeredImage {
        inherit name;
        tag = "nix";
        contents = [ pkgs.cacert ];
        config = {
          Entrypoint = [ "${pkg}/bin/${name}" ];
          User = "65534:65534";
        };
      };

      # panel imports ../profctl (replace in its go.mod), so its source holds both
      panel = pkgs.buildGoModule {
        pname = "panel";
        version = "0";
        src = pkgs.lib.fileset.toSource {
          root = ./.;
          fileset = pkgs.lib.fileset.unions [ ./services/panel ./services/profctl ];
        };
        modRoot = "services/panel";
        vendorHash = "sha256-+REK/1yn8tmUF4IADxO4jiozNMCzxC9ghRY1F9tGwyU=";
        env.CGO_ENABLED = "0";
        ldflags = [ "-s" "-w" ];
      };

      bins = builtins.mapAttrs bin goServices // { inherit panel; };

      # command line tools, no container image
      goTools = { profctl = "sha256-6RZzCcMKRaJ48KnTvzyRhJZOn8h8bwxYf+sqIm0a0JA="; };
      tools = builtins.mapAttrs bin goTools;
    in {
      packages.x86_64-linux = bins // tools
        // builtins.listToAttrs (map
          (n: { name = "image-${n}"; value = image n bins.${n}; })
          (builtins.attrNames bins));

      # nix develop: everything scripts/check needs
      devShells.x86_64-linux.default = pkgs.mkShell {
        packages = with pkgs; [ go kubectl kubeconform gitleaks skopeo ];
      };
    };
}
