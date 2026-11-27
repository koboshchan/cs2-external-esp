#include "net/ws_client.hpp"

// Must match the rest of the project: the vendored libcurl is a static library,
// otherwise the compiler expects dllimport stubs (__imp_curl_ws_send).
#ifndef CURL_STATICLIB
#define CURL_STATICLIB
#endif

#include <curl/curl.h>

#include <chrono>
#include <cstring>

#ifdef _WIN32
// <windows.h> is force-included by the project and already provides
// fd_set/select/FD_* for sockets. Including <winsock2.h> here would conflict.
#else
#include <sys/select.h>
#endif

namespace net
{
	namespace
	{
		void GlobalInit()
		{
			static const bool once = []
			{
				curl_global_init(CURL_GLOBAL_DEFAULT);
				return true;
			}();
			(void)once;
		}

		// libcurl hands us one chunk at a time; a message is complete when
		// bytesleft reaches zero.
		bool IsFinalChunk(const curl_ws_frame* meta)
		{
			return meta && meta->bytesleft == 0;
		}
	}

	WsClient::~WsClient()
	{
		Close();
	}

	std::string WsClient::RuntimeInfo()
	{
		GlobalInit();

		std::string out = "libcurl ";
		const curl_version_info_data* info = curl_version_info(CURLVERSION_NOW);
		if (!info)
			return out + "(unknown)";

		out += info->version ? info->version : "?";
		out += " protocols:";
		for (const char* const* p = info->protocols; p && *p; ++p)
		{
			out += ' ';
			out += *p;
		}
		return out;
	}

	bool WsClient::Connect(const std::string& url, std::string* error)
	{
		Close();
		GlobalInit();

		easy_ = curl_easy_init();
		if (!easy_)
		{
			if (error) *error = "curl init failed";
			return false;
		}

		curl_easy_setopt(easy_, CURLOPT_URL, url.c_str());
		curl_easy_setopt(easy_, CURLOPT_CONNECT_ONLY, 2L); // 2 = websocket
		curl_easy_setopt(easy_, CURLOPT_USERAGENT, "cs2-external-esp-net/1");
		curl_easy_setopt(easy_, CURLOPT_CONNECTTIMEOUT, 10L);
		curl_easy_setopt(easy_, CURLOPT_SSL_VERIFYPEER, 1L);
		curl_easy_setopt(easy_, CURLOPT_SSL_VERIFYHOST, 2L);
		curl_easy_setopt(easy_, CURLOPT_NOSIGNAL, 1L);
		curl_easy_setopt(easy_, CURLOPT_TCP_KEEPALIVE, 1L);

		const CURLcode rc = curl_easy_perform(easy_);
		if (rc != CURLE_OK)
		{
			long response = 0;
			curl_easy_getinfo(easy_, CURLINFO_RESPONSE_CODE, &response);
			if (error)
			{
				*error = std::string(curl_easy_strerror(rc)) +
					" (curl code " + std::to_string(int(rc)) +
					", http " + std::to_string(response) + ")";
			}
			Close();
			return false;
		}

		long response = 0;
		curl_easy_getinfo(easy_, CURLINFO_RESPONSE_CODE, &response);
		if (response != 101)
		{
			if (error) *error = "handshake failed (HTTP " + std::to_string(response) + ")";
			Close();
			return false;
		}

		connected_ = true;
		return true;
	}

	void WsClient::Close()
	{
		if (easy_)
			curl_easy_cleanup(easy_);
		easy_ = nullptr;
		connected_ = false;
		rx_.clear();
	}

	WsClient::WaitResult WsClient::Wait(bool readable, int timeout_ms)
	{
		if (!easy_ || !connected_)
			return WaitResult::Error;

		curl_socket_t sock = CURL_SOCKET_BAD;
		curl_easy_getinfo(easy_, CURLINFO_ACTIVESOCKET, &sock);
		if (sock == CURL_SOCKET_BAD)
			return WaitResult::Error;

		fd_set set;
		FD_ZERO(&set);
		FD_SET(sock, &set);

		timeval tv;
		tv.tv_sec = timeout_ms / 1000;
		tv.tv_usec = (timeout_ms % 1000) * 1000;

		const int rc = select(static_cast<int>(sock) + 1,
			readable ? &set : nullptr,
			readable ? nullptr : &set,
			nullptr,
			&tv);

		if (rc < 0)
			return WaitResult::Error;
		if (rc == 0)
			return WaitResult::Timeout;

		return WaitResult::Ready;
	}

	bool WsClient::SendText(const std::string& text)
	{
		return SendFrame(text.data(), text.size(), CURLWS_TEXT);
	}

	bool WsClient::SendBinary(const void* data, size_t size)
	{
		return SendFrame(data, size, CURLWS_BINARY);
	}

	bool WsClient::SendFrame(const void* data, size_t size, unsigned flags)
	{
		if (!connected_ || !easy_)
			return false;

		const auto* bytes = static_cast<const uint8_t*>(data);
		size_t offset = 0;
		const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(2);

		while (offset < size)
		{
			size_t sent = 0;
			const CURLcode rc = curl_ws_send(
				easy_,
				reinterpret_cast<const char*>(bytes + offset),
				size - offset,
				&sent,
				0,
				flags
			);

			if (rc == CURLE_AGAIN)
			{
				if (Wait(false, 50) != WaitResult::Ready || std::chrono::steady_clock::now() > deadline)
				{
					connected_ = false;
					return false;
				}
				continue;
			}

			if (rc != CURLE_OK)
			{
				connected_ = false;
				return false;
			}

			offset += sent;
		}

		return true;
	}

	bool WsClient::Poll(int timeout_ms)
	{
		if (!connected_ || !easy_)
			return false;

		switch (Wait(true, timeout_ms))
		{
		case WaitResult::Error:
			connected_ = false;
			return false;
		case WaitResult::Timeout:
			return connected_;
		case WaitResult::Ready:
			break;
		}

		DrainIncoming();
		return connected_;
	}

	void WsClient::DrainIncoming()
	{
		uint8_t buffer[16 * 1024];

		for (;;)
		{
			size_t received = 0;
			const curl_ws_frame* meta = nullptr;
			const CURLcode rc = curl_ws_recv(easy_, buffer, sizeof(buffer), &received, &meta);

			if (rc == CURLE_AGAIN)
				return;

			if (rc != CURLE_OK)
			{
				connected_ = false;
				return;
			}

			if (!meta)
				continue;

			if (meta->flags & CURLWS_CLOSE)
			{
				connected_ = false;
				return;
			}

			// Control frames are handled by libcurl itself.
			if (meta->flags & (CURLWS_PING | CURLWS_PONG))
				continue;

			if (received == 0)
				continue;

			if (rx_.empty())
				rx_binary_ = (meta->flags & CURLWS_BINARY) != 0;

			rx_.insert(rx_.end(), buffer, buffer + received);

			if (IsFinalChunk(meta))
			{
				if (on_message_ && !rx_.empty())
					on_message_(rx_binary_, rx_.data(), rx_.size());
				rx_.clear();
			}

			// Guard against a peer that never ends a message.
			if (rx_.size() > 1024 * 1024)
			{
				rx_.clear();
				connected_ = false;
				return;
			}
		}
	}
}
