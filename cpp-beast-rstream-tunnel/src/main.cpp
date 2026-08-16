#include <csignal>
#include <cstdlib>
#include <iostream>
#include <stdexcept>
#include <string>

#include <boost/asio.hpp>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>

#include <rstream/core/exception.hpp>
#include <rstream/io-rstrm/client.hpp>
#include <rstream/io-rstrm/io-rstrm.hpp>
#include <rstream/io-rstrm/socket.hpp>

namespace asio  = boost::asio;
namespace beast = boost::beast;
namespace http  = beast::http;
namespace rstrm = rstream::io_rstrm;

asio::awaitable<void> serve_session(rstrm::socket socket)
{
  try {
    beast::flat_buffer buffer;
    http::request<http::string_body> request;
    co_await http::async_read(socket, buffer, request, asio::use_awaitable);
    http::response<http::string_body> response{http::status::ok,
                                               request.version()};
    response.set(http::field::server, "cpp-beast-rstream-tunnel");
    response.set(http::field::content_type, "text/plain; charset=utf-8");
    response.keep_alive(request.keep_alive());
    response.body() = "Hello from Boost.Beast through rstream\n";
    response.prepare_payload();
    co_await http::async_write(socket, response, asio::use_awaitable);
    boost::system::error_code ignored;
    socket.close(ignored);
  }
  catch (const boost::system::system_error& error) {
    if (error.code() != http::error::end_of_stream && error.code() != asio::error::operation_aborted) {
      std::cerr << "session error: " << error.what() << std::endl;
    }
  }
  catch (const std::exception& error) {
    std::cerr << "session error: " << error.what() << std::endl;
  }
}

asio::awaitable<void> run_server(rstrm::client& client,
                                 const std::string& tunnel_name)
{
  auto executor = co_await asio::this_coro::executor;
  co_await client.async_connect(asio::use_awaitable);
  rstrm::tunnel_properties properties;
  properties.m_name         = tunnel_name;
  properties.m_publish      = true;
  properties.m_protocol     = rstrm::protocol::http;
  properties.m_http_version = std::string("http/1.1");
  properties.m_labels       = {
      {"framework", "boost-beast"},
      {"language", "cpp"},
      {"service", "http"},
  };
  auto tunnel     = co_await client.async_create_tunnel(properties, asio::use_awaitable);
  auto forwarding = rstrm::format_forwarding_address(tunnel.properties());
  if (!forwarding) {
    throw std::runtime_error("published tunnel has no forwarding address");
  }
  std::cout << "Forwarding address: " << forwarding.value() << std::endl;
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
  rstrm::client client(io_context.get_executor());
  const char* configured_name   = std::getenv("RSTREAM_TUNNEL_NAME");
  const std::string tunnel_name = configured_name && configured_name[0]
                                      ? configured_name
                                      : "cpp-beast-http";
  asio::signal_set signals(io_context, SIGINT, SIGTERM);
  bool shutting_down = false;
  signals.async_wait([&](const boost::system::error_code& error, int) {
    if (!error) {
      shutting_down = true;
      client.close();
    }
  });
  int exit_code = 0;
  asio::co_spawn(io_context, run_server(client, tunnel_name),
                 [&](std::exception_ptr error) {
                   if (error && !shutting_down) {
                     std::cerr << "fatal error: "
                               << rstream::core::throwable::to_string(error)
                               << std::endl;
                     exit_code = 1;
                   }
                   boost::system::error_code ignored;
                   signals.cancel(ignored);
                 });
  io_context.run();
  return exit_code;
}
