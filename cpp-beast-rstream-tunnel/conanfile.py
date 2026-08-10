from conan import ConanFile
from conan.tools.cmake import CMake, CMakeDeps, CMakeToolchain, cmake_layout


class CppBeastRstreamTunnel(ConanFile):
    name = "cpp-beast-rstream-tunnel"
    version = "1.0.0"
    settings = "os", "compiler", "build_type", "arch"
    exports_sources = "CMakeLists.txt", "src/*"
    requires = (
        # Keep this aligned with the SDK's supported Conan range. Boost 1.90
        # and 1.91 currently publish inconsistent Cobalt library metadata.
        "boost/[>=1.81.0 <1.90.0]",
        "rstream/[>=1.12.0 <2]",
    )
    default_options = {
        "boost/*:without_cobalt": True,
        "rstream/*:build_bins": False,
        "rstream/*:enable_testing": False,
        "rstream/*:with_maxminddb": False,
        "rstream/*:with_ncurses": False,
    }

    def layout(self):
        cmake_layout(self)

    def generate(self):
        CMakeToolchain(self).generate()
        CMakeDeps(self).generate()

    def build(self):
        cmake = CMake(self)
        cmake.configure()
        cmake.build()

    def package(self):
        cmake = CMake(self)
        cmake.install()
