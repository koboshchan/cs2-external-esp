#pragma once

// Relay endpoint resolution.
//
// Default: wss://cs2.kjt.lol/ws
// Override: CS2ESPSERVER env var (host, full ws:// URL, or http(s):// URL)
// Precedence: CLI flag > CS2ESPSERVER > config value > default.

#include <cstdlib>
#include <string>

namespace net
{
	inline bool StartsWith(const std::string& value, const char* prefix)
	{
		return value.rfind(prefix, 0) == 0;
	}

	// Accepts "host", "host:port", "ws://host", "https://host/path" and
	// returns a websocket URL with a path (defaults to /ws).
	inline std::string NormalizeWsUrl(std::string url)
	{
		const auto first = url.find_first_not_of(" \t\r\n");
		if (first == std::string::npos)
			return {};
		url = url.substr(first, url.find_last_not_of(" \t\r\n") - first + 1);
		std::string scheme = "wss://";
		std::string rest;

		if (StartsWith(url, "wss://"))
		{
			rest = url.substr(6);
		}
		else if (StartsWith(url, "ws://"))
		{
			scheme = "ws://";
			rest = url.substr(5);
		}
		else if (StartsWith(url, "https://"))
		{
			rest = url.substr(8);
		}
		else if (StartsWith(url, "http://"))
		{
			scheme = "ws://";
			rest = url.substr(7);
		}
		else
		{
			rest = url;
		}

		while (!rest.empty() && rest.back() == '/')
			rest.pop_back();

		const size_t slash = rest.find('/');
		if (slash == std::string::npos)
			rest += "/ws";
		else if (slash == rest.size() - 1)
			rest += "ws";

		return scheme + rest;
	}

	inline std::string EnvValue(const char* name)
	{
#ifdef _WIN32
		char* value = nullptr;
		size_t length = 0;
		if (_dupenv_s(&value, &length, name) == 0 && value)
		{
			std::string out(value);
			free(value);
			return out;
		}
		return {};
#else
		const char* value = std::getenv(name);
		return value ? value : "";
#endif
	}

	inline std::string ResolveServerUrl(const std::string& configured)
	{
		const std::string env = EnvValue("CS2ESPSERVER");
		const std::string raw = !env.empty() ? env : configured;

		if (raw.empty())
			return "wss://cs2.kjt.lol/ws";

		const std::string normalized = NormalizeWsUrl(raw);
		return normalized.empty() ? "wss://cs2.kjt.lol/ws" : normalized;
	}

	// https://host from wss://host/ws, used for POST /rooms.
	inline std::string HttpBaseFromWsUrl(const std::string& ws_url)
	{
		std::string url = ws_url;

		if (StartsWith(url, "wss://"))
			url = "https://" + url.substr(6);
		else if (StartsWith(url, "ws://"))
			url = "http://" + url.substr(5);

		const size_t scheme_end = url.find("://");
		const size_t slash = url.find('/', scheme_end == std::string::npos ? 0 : scheme_end + 3);
		if (slash != std::string::npos)
			url = url.substr(0, slash);

		return url;
	}
}
