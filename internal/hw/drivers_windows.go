package hw

// Minimum Windows driver versions for the CUDA builds (SPEC section 5), in NVIDIA's display version (such as
// 572.83). The Windows CUDA 12 build is 12.4, whose toolkit release notes name 551.61 as the minimum for the GA
// driver; CUDA 13.x needs the R580 branch. Not exercised on the reference laptop, which has no NVIDIA GPU.
const (
	minDriverCUDA12 = "551.61"
	minDriverCUDA13 = "580"
)
