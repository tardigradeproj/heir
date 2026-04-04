package controlplaneimage

// Option is a configuration option supplied to Build
type Option interface {
	apply(*buildContext) error
}

type optionAdapter func(*buildContext) error

func (c optionAdapter) apply(o *buildContext) error {
	return c(o)
}

// WithImage configures a build to tag the built image with `image`
func WithImage(image string) Option {
	return optionAdapter(func(b *buildContext) error {
		b.image = image
		return nil
	})
}

// WithBaseImage configures a build to use `image` as the base image
func WithBaseImage(image string) Option {
	return optionAdapter(func(b *buildContext) error {
		b.baseImage = image
		return nil
	})
}

// WithKubeParam sets the path to the Kubernetes source directory (if empty, the path will be autodetected)
func WithKubeParam(root string) Option {
	return optionAdapter(func(b *buildContext) error {
		b.kubeParam = root
		return nil
	})
}

// WithArch sets the architecture to build for
func WithArch(arch string) Option {
	return optionAdapter(func(b *buildContext) error {
		if arch != "" {
			b.arch = arch
		}
		return nil
	})
}

// WithArch sets the architecture to build for
func WithBuildType(buildType string) Option {
	return optionAdapter(func(b *buildContext) error {
		if buildType != "" {
			b.buildType = buildType
		}
		return nil
	})
}
