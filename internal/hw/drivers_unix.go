//go:build !windows

package hw

// Minimum Linux driver versions for the CUDA builds (SPEC section 5). CUDA 12.8 GA needs >=570.26;
// CUDA 13.x minor-version compatibility needs >=580. CUDA 13.4 features and newly enabled platforms can
// require R615 or newer: https://docs.nvidia.com/cuda/cuda-toolkit-release-notes/index.html. These baselines
// are not exercised on the reference laptop, which has no NVIDIA GPU. The catalog (WP1) must carry the same values.
const (
	minDriverCUDA12 = "570.26"
	minDriverCUDA13 = "580"
)
