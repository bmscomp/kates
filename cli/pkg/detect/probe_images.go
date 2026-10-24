package detect

// ProbeImages are the images the pre-flight's probe pods run, which a
// bundle carries with the stack's (kates charts pull --images).
var ProbeImages = []string{"alpine:3.19", "busybox:1.36", "busybox:1.37"}

// ProbeImageRegistry, when set, is the mirror the probe pods pull from:
// <registry>/<image>, the way kates charts push names each image.
var ProbeImageRegistry string

func probeImage(ref string) string {
	if ProbeImageRegistry == "" {
		return ref
	}
	return ProbeImageRegistry + "/" + ref
}
