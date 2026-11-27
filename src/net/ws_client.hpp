#pragma once

// Minimal WebSocket client.
//
// Implemented with WinHTTP, which is part of Windows: it gives us ws:// and
// wss:// (Schannel TLS) without adding a dependency. The vendored libcurl is
// built without WebSocket support, so it cannot be used for this.
//
// Single-threaded by design: one publisher thread owns one WsClient. Receive
// is polled (with a short timeout) rather than run on its own thread, so the
// caller controls how long a poll may block.

#include <cstdint>
#include <functional>
#include <string>
#include <vector>

namespace net
{
	class WsClient
	{
	public:
		using MessageCallback = std::function<void(bool binary, const uint8_t* data, size_t size)>;

		WsClient() = default;
		~WsClient();

		WsClient(const WsClient&) = delete;
		WsClient& operator=(const WsClient&) = delete;

		// Performs the HTTP upgrade and returns once the socket is a websocket.
		bool Connect(const std::string& url, std::string* error = nullptr);

		void Close();

		bool IsConnected() const { return connected_; }

		bool SendText(const std::string& text);
		bool SendBinary(const void* data, size_t size);

		// Receives at most one chunk (bounded by the receive timeout) and
		// dispatches complete messages. Returns false when the connection dropped.
		bool Poll(int timeout_ms);

		void SetOnMessage(MessageCallback cb) { on_message_ = std::move(cb); }

		static std::string RuntimeInfo();

	private:
		bool SendFrame(const void* data, size_t size, bool text);
		void DrainIncoming();

		void* session_ = nullptr;  // HINTERNET
		void* connect_ = nullptr;  // HINTERNET
		void* request_ = nullptr;  // HINTERNET
		void* websocket_ = nullptr; // HINTERNET
		bool connected_ = false;

		// Reassembly buffer for fragmented messages.
		std::vector<uint8_t> rx_;
		bool rx_binary_ = false;

		MessageCallback on_message_;
	};
}
