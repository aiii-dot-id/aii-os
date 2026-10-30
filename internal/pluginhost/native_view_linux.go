//go:build linux || android

package pluginhost

var DefaultNativeView = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/etc/ld.so.cache", "/etc/localtime",
	"/etc/vulkan", "/etc/OpenCL", "/etc/glvnd", "/etc/nvidia", "/etc/alternatives",
	"/sys",
}
