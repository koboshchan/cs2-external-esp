#pragma once

// Minimal WebSocket client over libcurl (already a project dependency).
//
// The vendored libcurl is 8.4.0-DEV with Schannel, so both ws:// and wss://
// work without adding a TLS library.
//
// libcurl 8.4 predates curl_easy_poll(), so readiness is detected with
// select() on CURLINFO_ACTIVESOCKET, which is the pattern used by libcurl's
// own websocket example.
//
// Single-threaded by design: one publisher thread owns one WsClient.

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

		// Pumps the connection for at most timeout_ms and dispatches complete
		// messages. Returns false when the connection dropped.
		bool Poll(int timeout_ms);

		void SetOnMessage(MessageCallback cb) { on_message_ = std::move(cb); }

		// libcurl version plus the protocols it was built with, for diagnostics.
		static std::string RuntimeInfo();

	private:
		enum class WaitResult { Ready, Timeout, Error };

		WaitResult Wait(bool readable, int timeout_ms);
		bool SendFrame(const void* data, size_t size, unsigned flags);
		void DrainIncoming();

		void* easy_ = nullptr; // CURL*
		bool connected_ = false;

		// Reassembly buffer for fragmented websocket messages.
		std::vector<uint8_t> rx_;
		bool rx_binary_ = false;

		MessageCallback on_message_;
	};
}
