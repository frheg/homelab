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

      # camview: the webcam as ASCII art in the terminal, with its own ffmpeg
      camview = (bin "camview" "sha256-/mTZkvmdSfFR+tBxuUKdDVqpxVAU7vxtcBS2yz2WBjI=").overrideAttrs (o: {
        ldflags = o.ldflags ++ [ "-X main.ffmpeg=${pkgs.ffmpeg-headless}/bin/ffmpeg" ];
      });

      # Piper voices, the same files and checksums as Athena's speech image (athena: speech/voices.txt)
      voice = path: sha256: pkgs.fetchurl {
        url = "https://huggingface.co/rhasspy/piper-voices/resolve/e9ab671f2a0e50a701b34361136ba3e6a3a9eb0c/${path}";
        inherit sha256;
      };
      voices = pkgs.linkFarm "piper-voices" (pkgs.lib.mapAttrsToList (name: v: { inherit name; path = voice v.path v.sha256; }) {
        "no_NO-talesyntese-medium.onnx" = { path = "no/no_NO/talesyntese/medium/no_NO-talesyntese-medium.onnx"; sha256 = "f5f33d4e2a67cbef7cc5bab6d4b773a815e6a5cb02438509ea9b7493696f539f"; };
        "no_NO-talesyntese-medium.onnx.json" = { path = "no/no_NO/talesyntese/medium/no_NO-talesyntese-medium.onnx.json"; sha256 = "48a75caac2cd2515e85aa4bc3fbf6c2c5f3b663b811291ca0a71628a51b05865"; };
        "no_NO-nvcc-medium.onnx" = { path = "no/no_NO/nvcc/medium/no_NO-nvcc-medium.onnx"; sha256 = "3f51702946d9544898440a1a66906155661a9d680ee103130ff68f73360b512e"; };
        "no_NO-nvcc-medium.onnx.json" = { path = "no/no_NO/nvcc/medium/no_NO-nvcc-medium.onnx.json"; sha256 = "98410e911e5a9076ac4677a79b08b20f98c54f550c4388e29978f6bafa32b83d"; };
        "en_GB-cori-high.onnx" = { path = "en/en_GB/cori/high/en_GB-cori-high.onnx"; sha256 = "470b4dd634c98f8a4850d7626ffc3dfc90774628eeef6605a6dd8f88f30a5903"; };
        "en_GB-cori-high.onnx.json" = { path = "en/en_GB/cori/high/en_GB-cori-high.onnx.json"; sha256 = "9e7fb5b5671612c22f3c81cbe46c1ae87b031a4632bcb509e499dad6f1e2adec"; };
      });

      # say: speaks text through kratos's speaker (scripts/say)
      say = pkgs.writeShellApplication {
        name = "say";
        runtimeInputs = with pkgs; [ piper-tts pipewire bluez gawk gnugrep gnused coreutils ];
        runtimeEnv.VOICES = voices;
        text = builtins.readFile ./scripts/say;
      };

      # athena-live: talking with Athena on kratos (scripts/athena-live), with speech recognition and its model
      whisperSmall = pkgs.fetchurl {
        url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-small-q5_1.bin";
        sha256 = "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb";
      };
      athena-live = pkgs.writeShellApplication {
        name = "athena-live";
        runtimeInputs = with pkgs; [ whisper-cpp curl coreutils ];
        runtimeEnv.WHISPER_MODEL = whisperSmall;
        text = builtins.readFile ./scripts/athena-live;
      };

      tools = builtins.mapAttrs bin goTools // { inherit camview say athena-live; };
    in {
      packages.x86_64-linux = bins // tools
        // builtins.listToAttrs (map
          (n: { name = "image-${n}"; value = image n bins.${n}; })
          (builtins.attrNames bins));

      # nix develop: everything scripts/check needs
      devShells.x86_64-linux.default = pkgs.mkShell {
        packages = with pkgs; [ go kubectl kubeconform gitleaks skopeo victoriametrics prometheus-alertmanager ];
      };
    };
}
