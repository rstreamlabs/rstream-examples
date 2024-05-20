// See LICENSE file in the project root for license information.

#include <cstdlib>
#include <iostream>
#include <string>
#include <vector>

#include <boost/asio/co_spawn.hpp>
#include <boost/asio/detached.hpp>
#ifndef RSTREAM_WITH_IO_STREAMS
#include <boost/asio/ip/tcp.hpp>
#endif
#include <boost/asio/signal_set.hpp>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>
#include <boost/beast/version.hpp>

#ifdef RSTREAM_WITH_IO_STREAMS
#include <rstream/io/stream.hpp>
#endif
#include <docopt/docopt.h>
#include <unistd.h>

#include <rstream/core/exception.hpp>
#include <rstream/io/address.hpp>

#ifdef RSTREAM_WITH_IO_STREAMS
using protocol = rstream::io::stream;
#else
using protocol = boost::asio::ip::tcp;
#endif

static const char USAGE[] = R"(
rstream-basic-http-server

usage:
  rstream-basic-http-server [options]
  rstream-basic-http-server (-h|--help)
  rstream-basic-http-server --version

options:
  -h --help            show this screen
  --version            show version
  --uri=ARG            URI [default: tcp://0.0.0.0:8080]
)";

const auto version = std::string("rstream-basic-http-server");

boost::asio::awaitable<void> session(protocol::socket socket)
{
  try {
    std::cout << "New connection from " << socket.remote_endpoint() << std::endl;

    // Read the request
    boost::beast::http::request<boost::beast::http::string_body> req;
    boost::beast::flat_buffer buffer;
    co_await boost::beast::http::async_read(socket, buffer, req, boost::asio::use_awaitable);

    // Prepare the response
    boost::beast::http::response<boost::beast::http::string_body> res;
    char hostname[1024];
    gethostname(hostname, 1024);
    res = {boost::beast::http::status::ok, req.version()};
    res.set(boost::beast::http::field::server, BOOST_BEAST_VERSION_STRING);
    res.set(boost::beast::http::field::content_type, "text/plain");
    res.keep_alive(req.keep_alive());
    res.body() = hostname;
    res.prepare_payload();

    // Write the response back to the client
    co_await boost::beast::http::async_write(socket, res, boost::asio::use_awaitable);
  }
  catch (std::exception const &e) {
    std::cerr << "An error occured when processing the request: " << e.what() << std::endl;
  }
}

boost::asio::awaitable<void> listener(const rstream::io::address &address)
{
  auto executor = co_await boost::asio::this_coro::executor;
#ifdef RSTREAM_WITH_IO_STREAMS
  const auto endpoints = co_await protocol::resolver(executor).async_resolve(address.m_uri, boost::asio::use_awaitable);
#else
  const auto endpoints = co_await protocol::resolver(executor).async_resolve(address.m_host, address.m_port, boost::asio::use_awaitable);
#endif
  if (endpoints.empty()) {
    throw std::runtime_error("No valid endpoints found");
  }
  protocol::acceptor acceptor(executor, endpoints.begin()->endpoint());
  std::cout << "Server started on " << acceptor.local_endpoint() << std::endl;
  while (true) {
    auto socket = co_await acceptor.async_accept(boost::asio::use_awaitable);
    boost::asio::co_spawn(executor, session(std::move(socket)), boost::asio::detached);
  }
}

int main(int argc, char **argv)
{
  auto args = docopt::docopt(USAGE, {argv + 1, argv + argc}, true, version);

  boost::asio::io_context io_context;

  boost::asio::signal_set signal_set(io_context, SIGINT, SIGTERM);

  auto handler = [&io_context](const std::exception_ptr exception_ptr) {
    if (exception_ptr) {
      std::cerr << "Server error: " << rstream::core::throwable::to_string(exception_ptr) << std::endl;
    }
    io_context.stop();
  };

  signal_set.async_wait(std::bind(handler, nullptr));

  boost::asio::co_spawn(io_context, listener(args.at("--uri").asString()), handler);

  io_context.run();

  std::cout << "Server stopped" << std::endl;
}
