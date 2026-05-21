#include <csignal>
#include <iostream>
#include <string>

#include <boost/asio.hpp>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>

#include <rstream/core/exception.hpp>
#include <rstream/io-rstrm/client.hpp>
#include <rstream/io-rstrm/io-rstrm.hpp>
#include <rstream/io-rstrm/socket.hpp>

namespace asio = boost::asio;
namespace beast = boost::beast;
namespace http = beast::http;
namespace rstrm = rstream::io_rstrm;

asio::awaitable<void> serve_session(rstrm::socket socket)
{
  try {
    beast::flat_buffer buffer;
    http::request<http::string_body> request;
    co_await http::async_read(socket, buffer, request, asio::use_awaitable);
    http::response<http::string_body> response{http::status::ok, request.version()};
    response.set(http::field::server, "cpp-beast-rstream-tunnel");
    response.set(http::field::content_type, "text/plain; charset=utf-8");
    response.keep_alive(request.keep_alive());
    response.body() = "Hello from Boost.Beast through rstream\n";
    response.prepare_payload();
    co_await http::async_write(socket, response, asio::use_awaitable);
    boost::system::error_code ignored;
    socket.close(ignored);
  }
  catch (const std::exception& error) {
    std::cerr << "session error: " << error.what() << std::endl;
  }
}

asio::awaitable<void> run_server()
{
  auto executor = co_await asio::this_coro::executor;
  rstrm::client client(executor);
  co_await client.async_connect(asio::use_awaitable);
  rstrm::tunnel_properties properties;
  properties.m_name = std::string("cpp-beast-http");
  properties.m_publish = true;
  properties.m_protocol = rstrm::protocol::http;
  properties.m_http_version = std::string("http/1.1");
  properties.m_labels = {
      {"framework", "boost-beast"},
      {"language", "cpp"},
      {"service", "http"},
  };
  auto tunnel = co_await client.async_create_tunnel(properties, asio::use_awaitable);
  auto forwarding = rstrm::format_forwarding_address(tunnel.properties());
  if (forwarding) {
    std::cout << "Forwarding address: " << forwarding.value() << std::endl;
  }
  for (;;) {
    rstrm::socket socket(executor);
    rstrm::endpoint peer;
    co_await tunnel.async_accept(socket, peer, asio::use_awaitable);
    asio::co_spawn(executor, serve_session(std::move(socket)), asio::detached);
  }
}

int main()
{
  asio::io_context io_context;
  asio::signal_set signals(io_context, SIGINT, SIGTERM);
  signals.async_wait([&](const boost::system::error_code&, int) {
    io_context.stop();
  });
  asio::co_spawn(io_context, run_server(), [&](std::exception_ptr error) {
    if (error) {
      std::cerr << "fatal error: " << rstream::core::throwable::to_string(error) << std::endl;
    }
    io_context.stop();
  });
  io_context.run();
  return 0;
}
