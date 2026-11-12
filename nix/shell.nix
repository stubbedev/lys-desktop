{inputs, ...}: {
  perSystem = {
    inputs',
    lib,
    pkgs,
    ...
  }: let
    gpuDrivers = inputs.lys-lib.lib.gpuDriverEnv pkgs;
    fonts = [pkgs.inter pkgs.jetbrains-mono pkgs.nerd-fonts.jetbrains-mono];
    fontDirs = lib.concatMapStrings (font: "  <dir>${font}/share/fonts</dir>\n") fonts;
  in {
    devShells.default = pkgs.mkShell {
      packages = with pkgs;
        [
          inputs'.devtools.packages.go
          golangci-lint
          gofumpt
          gopls
          delve
          just
          git
          gh
          "${inputs.lys-ci}"
          fontconfig
          sway
          dbus
          pipewire
          systemd
          inputs.lys-wm.packages.${pkgs.stdenv.hostPlatform.system}.default
          wl-clipboard
        ]
        ++ fonts;

      LD_LIBRARY_PATH = lib.makeLibraryPath (lib.unique (gpuDrivers.libraries ++ [pkgs.libva pkgs.vulkan-loader pkgs.pipewire pkgs.libei]));
      XDG_DESKTOP_PORTAL = "${pkgs.xdg-desktop-portal}/libexec/xdg-desktop-portal";
      XKB_CONFIG_ROOT = "${pkgs.xkeyboard-config}/share/X11/xkb";
      XLOCALEDIR = "${pkgs.libX11}/share/X11/locale";
      CGO_ENABLED = "0";
      GOTOOLCHAIN = "local";

      shellHook =
        ''
          fontconf="''${TMPDIR:-/tmp}/lys-desktop-fonts.conf"
          cat > "$fontconf" <<FONTS
          <?xml version="1.0"?>
          <!DOCTYPE fontconfig SYSTEM "fonts.dtd">
          <fontconfig>
            <include ignore_missing="yes">/etc/fonts/fonts.conf</include>
          ${fontDirs}  <cachedir>''${XDG_CACHE_HOME:-$HOME/.cache}/fontconfig</cachedir>
          </fontconfig>
          FONTS
          export FONTCONFIG_FILE="$fontconf"
        ''
        + gpuDrivers.shellHook;
    };

    formatter = pkgs.alejandra;
  };
}
