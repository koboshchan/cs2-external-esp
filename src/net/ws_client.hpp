#pragma once

// Minimal WebSocket client.
//
// Implemented with WinHTTP, which is part of Windows: it gives us ws:// and
// wss:// (Schannel TLS) without adding a dependency. The vendored libcurl is
// built without WebSocket support, so it cannot be used for this.
//
// WinHttpWebSocketReceive blocks and WinHTTP does not honour a short receive
// timeout on the websocket handle, so receiving runs on its own thread while
// the publisher thread only sends. Control messages (welcome/error) are
// delivered through the message callback.

#include <atomic>
#include <cstdint>
#include <functional>
#include <string>
#include <thread>
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

		// Performs the HTTP upgrade and starts the receive thread.
		bool Connect(const std::string& url, std::string* error = nullptr);

		void Close();

		bool IsConnected() const { return connected_; }

		bool SendText(const std::string& text);
		bool SendBinary(const void* data, size_t size);

		// Kept for call-site compatibility; receiving happens on its own thread.
		bool Poll(int timeout_ms);

		void SetOnMessage(MessageCallback cb);

		static std::string RuntimeInfo();

	private:
		bool SendFrame(const void* data, size_t size, bool text);
		void ReceiveLoop();
		bool ReceiveOnce();
		void Dispatch(const uint8_t* data, size_t size, bool binary);

		void* session_ = nullptr;   // HINTERNET
		void* connect_ = nullptr;   // HINTERNET
		void* request_ = nullptr;   // HINTERNET
		void* websocket_ = nullptr; // HINTERNET

		std::atomic<bool> connected_{ false };
		std::atomic<bool> stop_{ false };
		std::thread receive_thread_;

		// Reassembly buffer for fragmented messages (receive thread only).
		std::vector<uint8_t> rx_;
		bool rx_binary_ = false;

		MessageCallback on_message_;
	};
}
