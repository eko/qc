# Homebrew formula for qc, published in the eko/homebrew-tap repository as
# Formula/qc.rb:
#
#   brew install eko/tap/qc
#
# Each release updates url and sha256 (see RELEASING.md).
class Qc < Formula
  desc "Fast video analysis: technical metrics, VMAF and per-title streaming ladders"
  homepage "https://github.com/eko/qc"
  url "https://github.com/eko/qc/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "09134539ac262b6c3c0e8c5eab0d56d6e47e78cbdadc464d77c735fe06bce0f1"
  license "MIT"
  head "https://github.com/eko/qc.git", branch: "main"

  depends_on "go" => :build
  depends_on "pkgconf" => :build # pkg-config, for the cgo binding to libvmaf
  depends_on "ffmpeg" # built with libx264, libx265 and SVT-AV1
  # libvmaf ≥ 3.2.1 (3.2.0 cannot load the VMAF v1 models); Homebrew's
  # formula installs the v1.0.16 models into share/libvmaf/model.
  depends_on "libvmaf"

  def install
    ENV["CGO_ENABLED"] = "1"

    # std_go_args already strips the binary (-s -w).
    ldflags = %W[
      -X main.version=v#{version}
      -X main.date=#{time.iso8601}
    ]
    system "go", "build", *std_go_args(ldflags:), "./cmd/qc"

    # qc looks for the models in /opt/homebrew/share, /usr/local/share and
    # /usr/share: point it at Linuxbrew's prefix, unless QC_MODEL_DIR is set.
    return unless OS.linux?

    libexec.install bin/"qc"
    model_dir = Formula["libvmaf"].opt_share/"libvmaf/model"
    (bin/"qc").write_env_script libexec/"qc", QC_MODEL_DIR: "${QC_MODEL_DIR:-#{model_dir}}"
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/qc version")

    ffmpeg = formula_opt_bin("ffmpeg")/"ffmpeg"
    ffprobe = formula_opt_bin("ffmpeg")/"ffprobe"
    tools = ["--ffmpeg", ffmpeg, "--ffprobe", ffprobe]

    # Every encoder and the VMAF v1 model must be available.
    system bin/"qc", "version", "--check", *tools

    system ffmpeg, "-hide_banner", "-loglevel", "error",
           "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=1",
           "-c:v", "libx264", "-crf", "10", "-pix_fmt", "yuv420p", "source.mp4"
    system ffmpeg, "-hide_banner", "-loglevel", "error",
           "-i", "source.mp4", "-c:v", "libx264", "-crf", "40", "encode.mp4"

    # stderr joins the output: were it brew's terminal, qc would draw its
    # live dashboard there from the test's background process group.
    output = shell_output("#{bin}/qc vmaf source.mp4 encode.mp4 --exact -f json #{tools.join(" ")} 2>&1")
    assert_match "vmaf_v1.0.16_3d0h", output
  end
end
