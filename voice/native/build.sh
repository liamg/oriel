#!/usr/bin/env bash
# Builds whisper.cpp at the pinned release as static libraries into lib/<goos>_<goarch>/, with its headers in include/,
# for the whisper_native build tag (cgo). Needs cmake ($CMAKE or on PATH) and a C/C++ compiler. A Mac builds both
# darwin targets; Linux builds its own arch. Metal on Apple silicon, the CPU elsewhere, with no -march=native: x86-64
# gets AVX2/FMA/F16C/BMI2, which the Go side checks for before loading a model. Not committed, like libghostty-vt.
# Pass targets (linux_amd64…) to build only those.
set -euo pipefail
cd "$(dirname "$0")"
. ./fetch.sh
cmake=${CMAKE:-cmake}

flags() {
	case "$1" in
	darwin_arm64) echo -DCMAKE_OSX_ARCHITECTURES=arm64 -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 ;;
	darwin_amd64) echo -DCMAKE_OSX_ARCHITECTURES=x86_64 -DGGML_METAL=OFF -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 ;;
	linux_amd64 | linux_arm64) echo ;;
	*) echo "no target $1" >&2; exit 1 ;;
	esac
}
targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
	case "$(uname -s)" in
	Darwin) targets=(darwin_arm64 darwin_amd64) ;;
	*) case "$(uname -m)" in aarch64 | arm64) targets=(linux_arm64) ;; *) targets=(linux_amd64) ;; esac ;;
	esac
fi
for t in "${targets[@]}"; do flags "$t" >/dev/null; done
rm -rf include && mkdir include
cp "$work/src/include/whisper.h" "$work"/src/ggml/include/*.h include/
for t in "${targets[@]}"; do
	b="$work/build-$t"
	# shellcheck disable=SC2046
	"$cmake" -S "$work/src" -B "$b" -Wno-dev -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
		-DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_SERVER=OFF -DWHISPER_ALL_WARNINGS=OFF \
		-DGGML_NATIVE=OFF -DGGML_OPENMP=OFF -DGGML_BLAS=OFF -DGGML_LLAMAFILE=OFF -DGGML_CCACHE=OFF -DGGML_ALL_WARNINGS=OFF \
		$(flags "$t") >/dev/null
	"$cmake" --build "$b" --config Release --parallel "$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)" >/dev/null
	rm -rf "lib/$t" && mkdir -p "lib/$t"
	find "$b" \( -name libwhisper.a -o -name 'libggml*.a' \) -exec cp {} "lib/$t/" \;
	echo "built lib/$t: $(cd "lib/$t" && ls | tr '\n' ' ')"
done
