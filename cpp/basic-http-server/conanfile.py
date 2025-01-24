#!/usr/bin/env python
# -*- coding: utf-8 -*-

from conan import ConanFile
import conan.tools.cmake

class ConanPackage(ConanFile):
    name = "rstream-basic-http-server"
    version = "1.0.0"
    settings = "os", "compiler", "build_type", "arch"
    exports_sources = "*"
    requires = \
        "boost/[]", \
        "rstream/[]"
        
    def generate(self):
        cmake_toolchain = conan.tools.cmake.CMakeToolchain(self)
        cmake_toolchain.generate()
        cmake_deps = conan.tools.cmake.CMakeDeps(self)
        cmake_deps.generate()
        
    def layout(self):
        conan.tools.cmake.cmake_layout(self)

    def build(self):
        cmake = conan.tools.cmake.CMake(self)
        cmake.configure()
        cmake.build()

    def package(self):
        cmake = conan.tools.cmake.CMake(self)
        cmake.install()
