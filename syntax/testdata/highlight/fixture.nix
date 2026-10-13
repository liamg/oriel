# A dev shell for the pricing service.
{ pkgs ? import <nixpkgs> { } }:

let
  version = "1.2.0";
  tools = with pkgs; [ go gopls golangci-lint ];
in
pkgs.mkShell {
  name = "pricing-${version}";
  buildInputs = tools ++ [ pkgs.postgresql_16 ];
  shellHook = ''
    export PGDATA=$PWD/.pg
  '';
  enableTests = true;
}
