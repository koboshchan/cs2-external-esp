#include "net/ws_client.hpp"

#include <windows.h>
#include <winhttp.h>

#pragma comment(lib, "winhttp.lib")

namespace net
{
	namespace
	{
		std::wstring Widen(const std::string& s)
		{
			if (s.empty())
				return {};

			const int needed = MultiByteToWideChar(CP_UTF8, 0, s.c_str(), int(s.size()), nullptr, 0);
			std::wstring out(size_t(needed), L'\0');
			MultiByteToWideChar(CP_UTF8, 0, s.c_str(), int(s.size()), out.data(), needed);
			return out;
		}

		// WinHttpCrackUrl only understands http/https; ws/wss are the same
		// scheme with an upgrade.
		std::string ToHttpScheme(const std::string& url, bool* secure)
		{
			if (url.rfind("wss://", 0) == 0)
			{
				if (secure) *secure = true;
				return "https://" + url.substr(6);
			}
			if (url.rfind("ws://", 0) == 0)
			{
				if (secure) *secure = false;
				return "http://" + url.substr(5);
			}
			if (secure) *secure = url.rfind("https://", 0) == 0;
			return url;
		}
	}

	WsClient::~WsClient()
	{
		Close();
	}

	std::string WsClient::RuntimeInfo()
	{
		return "WinHTTP websocket (ws/wss)";
	}

	bool WsClient::Connect(const std::string& url, std::string* error)
	{
		Close();

		bool secure = false;
		const std::wstring wide_url = Widen(ToHttpScheme(url, &secure));

		URL_COMPONENTS uc{};
		uc.dwStructSize = sizeof(uc);
		uc.dwSchemeLength = DWORD(-1);
		uc.dwHostNameLength = DWORD(-1);
		uc.dwUrlPathLength = DWORD(-1);
		uc.dwExtraInfoLength = DWORD(-1);

		if (!WinHttpCrackUrl(wide_url.c_str(), DWORD(wide_url.size()), 0, &uc))
		{
			if (error) *error = "invalid url";
			return false;
		}

		std::wstring host(uc.lpszHostName, uc.dwHostNameLength);
		std::wstring path(uc.lpszUrlPath, uc.dwUrlPathLength);
		if (uc.dwExtraInfoLength)
			path.append(uc.lpszExtraInfo, uc.dwExtraInfoLength);
		if (path.empty())
			path = L"/";

		session_ = WinHttpOpen(
			L"cs2-external-esp-net/1",
			WINHTTP_ACCESS_TYPE_NO_PROXY,
			WINHTTP_NO_PROXY_NAME,
			WINHTTP_NO_PROXY_BYPASS,
			0
		);

		if (!session_)
		{
			if (error) *error = "WinHttpOpen failed";
			Close();
			return false;
		}

		// resolve, connect, send, receive (ms)
		WinHttpSetTimeouts(session_, 5000, 10000, 2000, 50);

		connect_ = WinHttpConnect(session_, host.c_str(), uc.nPort, 0);
		if (!connect_)
		{
			if (error) *error = "WinHttpConnect failed";
			Close();
			return false;
		}

		request_ = WinHttpOpenRequest(
			connect_,
			L"GET",
			path.c_str(),
			nullptr,
			WINHTTP_NO_REFERER,
			WINHTTP_DEFAULT_ACCEPT_TYPES,
			secure ? WINHTTP_FLAG_SECURE : 0
		);

		if (!request_)
		{
			if (error) *error = "WinHttpOpenRequest failed";
			Close();
			return false;
		}

		BOOL upgrade = TRUE;
		if (!WinHttpSetOption(request_, WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET, &upgrade, sizeof(upgrade)))
		{
			if (error) *error = "WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET failed (" + std::to_string(GetLastError()) + ")";
			Close();
			return false;
		}

		if (!WinHttpSendRequest(request_, WINHTTP_NO_ADDITIONAL_HEADERS, 0, nullptr, 0, 0, 0))
		{
			if (error) *error = "WinHttpSendRequest failed (" + std::to_string(GetLastError()) + ")";
			Close();
			return false;
		}

		if (!WinHttpReceiveResponse(request_, nullptr))
		{
			if (error) *error = "WinHttpReceiveResponse failed (" + std::to_string(GetLastError()) + ")";
			Close();
			return false;
		}

		DWORD status = 0;
		DWORD status_size = sizeof(status);
		WinHttpQueryHeaders(
			request_,
			WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
			WINHTTP_HEADER_NAME_BY_INDEX,
			&status,
			&status_size,
			WINHTTP_NO_HEADER_INDEX
		);

		if (status != 101)
		{
			if (error) *error = "handshake failed (HTTP " + std::to_string(status) + ")";
			Close();
			return false;
		}

		websocket_ = WinHttpWebSocketCompleteUpgrade(request_, 0);
		if (!websocket_)
		{
			if (error) *error = "websocket upgrade failed (" + std::to_string(GetLastError()) + ")";
			Close();
			return false;
		}

		// The request handle is done once the websocket exists.
		WinHttpCloseHandle(static_cast<HINTERNET>(request_));
		request_ = nullptr;

		connected_ = true;
		return true;
	}

	void WsClient::Close()
	{
		if (websocket_)
		{
			WinHttpWebSocketClose(static_cast<HINTERNET>(websocket_), WINHTTP_WEB_SOCKET_SUCCESS_CLOSE_STATUS, nullptr, 0);
			WinHttpCloseHandle(static_cast<HINTERNET>(websocket_));
			websocket_ = nullptr;
		}
		if (request_)
		{
			WinHttpCloseHandle(static_cast<HINTERNET>(request_));
			request_ = nullptr;
		}
		if (connect_)
		{
			WinHttpCloseHandle(static_cast<HINTERNET>(connect_));
			connect_ = nullptr;
		}
		if (session_)
		{
			WinHttpCloseHandle(static_cast<HINTERNET>(session_));
			session_ = nullptr;
		}

		connected_ = false;
		rx_.clear();
	}

	bool WsClient::SendText(const std::string& text)
	{
		return SendFrame(text.data(), text.size(), true);
	}

	bool WsClient::SendBinary(const void* data, size_t size)
	{
		return SendFrame(data, size, false);
	}

	bool WsClient::SendFrame(const void* data, size_t size, bool text)
	{
		if (!connected_ || !websocket_)
			return false;

		if (size > 0xffffffffull)
		{
			connected_ = false;
			return false;
		}

		const DWORD error = WinHttpWebSocketSend(
			static_cast<HINTERNET>(websocket_),
			text ? WINHTTP_WEB_SOCKET_UTF8_MESSAGE_BUFFER_TYPE : WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE,
			const_cast<void*>(data),
			DWORD(size)
		);

		if (error != ERROR_SUCCESS)
		{
			connected_ = false;
			return false;
		}

		return true;
	}

	bool WsClient::Poll(int timeout_ms)
	{
		(void)timeout_ms; // the receive timeout is configured on the session

		if (!connected_ || !websocket_)
			return false;

		DrainIncoming();
		return connected_;
	}

	void WsClient::DrainIncoming()
	{
		uint8_t buffer[16 * 1024];

		for (;;)
		{
			DWORD received = 0;
			WINHTTP_WEB_SOCKET_BUFFER_TYPE type = WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE;

			const DWORD error = WinHttpWebSocketReceive(
				static_cast<HINTERNET>(websocket_),
				buffer,
				DWORD(sizeof(buffer)),
				&received,
				&type
			);

			// No data within the receive timeout: normal for a quiet relay.
			if (error == ERROR_WINHTTP_TIMEOUT)
				return;

			if (error != ERROR_SUCCESS)
			{
				connected_ = false;
				return;
			}

			if (type == WINHTTP_WEB_SOCKET_CLOSE_BUFFER_TYPE)
			{
				connected_ = false;
				return;
			}

			if (type != WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE &&
				type != WINHTTP_WEB_SOCKET_BINARY_FRAGMENT_BUFFER_TYPE &&
				type != WINHTTP_WEB_SOCKET_UTF8_MESSAGE_BUFFER_TYPE &&
				type != WINHTTP_WEB_SOCKET_UTF8_FRAGMENT_BUFFER_TYPE)
				return;

			if (received == 0)
				continue;

			const bool fragment =
				type == WINHTTP_WEB_SOCKET_UTF8_FRAGMENT_BUFFER_TYPE ||
				type == WINHTTP_WEB_SOCKET_BINARY_FRAGMENT_BUFFER_TYPE;

			if (rx_.empty())
				rx_binary_ = (type == WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE ||
					type == WINHTTP_WEB_SOCKET_BINARY_FRAGMENT_BUFFER_TYPE);

			rx_.insert(rx_.end(), buffer, buffer + received);

			if (!fragment)
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
