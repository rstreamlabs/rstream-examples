cmake_minimum_required(VERSION 3.22)
if(POLICY CMP0167)
  cmake_policy(SET CMP0167 NEW)
endif()

project(cpp_beast_rstream_tunnel LANGUAGES CXX)

find_package(Boost REQUIRED)
find_package(rstream CONFIG REQUIRED)

add_executable(cpp_beast_rstream_tunnel src/main.cpp)
target_compile_features(cpp_beast_rstream_tunnel PRIVATE cxx_std_20)
target_link_libraries(cpp_beast_rstream_tunnel PRIVATE rstream::rstream Boost::boost)
install(TARGETS cpp_beast_rstream_tunnel RUNTIME DESTINATION bin)
