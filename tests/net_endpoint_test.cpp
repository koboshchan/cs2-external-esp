#include "net/endpoint.hpp"
#include <cassert>
int main() {
 assert(net::NormalizeWsUrl("  http://127.0.0.1:8080/\n") == "ws://127.0.0.1:8080/ws");
 assert(net::NormalizeWsUrl(" example.org ") == "wss://example.org/ws");
 assert(net::NormalizeWsUrl("  ").empty());
 assert(net::HttpBaseFromWsUrl("ws://127.0.0.1:8080/ws") == "http://127.0.0.1:8080");
}
