from conan import ConanFile
from conan.tools.cmake import CMake, CMakeDeps, CMakeToolchain, cmake_layout


class CppBeastRstreamTunnel(ConanFile):
    name = "cpp-beast-rstream-tunnel"
    version = "1.0.0"
    settings = "os", "compiler", "build_type", "arch"
    exports_sources = "CMakeLists.txt", "src/*"
    requires = (
        "boost/[>=1.83 <2]",
        "rstream/[>=1.10.0 <2]",
    )
    default_options = {
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
