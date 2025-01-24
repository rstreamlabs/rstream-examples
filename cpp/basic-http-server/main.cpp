#include <cstdlib>
#include <iostream>

#include <boost/asio.hpp>
#include <boost/beast.hpp>

#include <rstream/rstream.hpp>

namespace rstrm = rstream::io_rstrm;

boost::asio::awaitable<void> session(rstrm::socket socket)
{
  try {
    std::cout << "New connection from [" << socket.remote_endpoint() << "]" << std::endl;
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
    socket.close();
  }
  catch (std::exception const& e) {
    std::cerr << "An error occured when processing the request: " << e.what() << std::endl;
  }
}

boost::asio::awaitable<void> listener()
{
  auto executor = co_await boost::asio::this_coro::executor;
  rstrm::client client(executor);
  co_await client.async_connect(boost::asio::use_awaitable);  // Connect to the default engine server
  struct rstrm::tunnel_properties properties = {
      .m_publish  = true,                   // Publish the tunnel
      .m_protocol = rstrm::protocol::http,  // HTTP tunnel
  };
  auto tunnel = co_await client.async_create_tunnel(properties, boost::asio::use_awaitable);
  std::cout << "Server listening on " << rstrm::format_forwarding_address(tunnel.properties()).value() << std::endl;
  rstrm::socket socket(executor);
  while (true) {
    rstrm::endpoint peer;
    co_await tunnel.async_accept(socket, peer, boost::asio::use_awaitable);
    boost::asio::co_spawn(executor, session(std::move(socket)), boost::asio::detached);
  }
}

int main()
{
#ifdef DEBUG_BUILD
  rstream::core::log::enable_ansicolor_stdout_mt();
#endif
  boost::asio::io_context io_context;
  boost::asio::signal_set signal_set(io_context, SIGINT, SIGTERM);
  auto handler = [&io_context](const std::exception_ptr& exception_ptr) {
    if (exception_ptr) {
      std::cerr << "Server error: " << rstream::core::throwable::to_string(exception_ptr) << std::endl;
    }
    io_context.stop();
  };
  signal_set.async_wait(std::bind(handler, nullptr));
  boost::asio::co_spawn(io_context, listener(), handler);
  io_context.run();
}
