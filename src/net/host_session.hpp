#pragma once

// Host side session: creates a room on the relay (POST /rooms), keeps the
// codes, and runs the publisher. Used by the Host menu tab and by
// --mode=host.

#include <string>

namespace net
{
	struct HostStatus
	{
		bool active = false;
		bool connected = false;
		unsigned long long frames_sent = 0;
		unsigned long long bytes_sent = 0;
		unsigned long long dropped = 0;

		std::string server;
		std::string room;
		std::string host_token;
		std::string viewer_key;
		std::string error;
	};

	class HostSession
	{
	public:
		// POST /rooms. On success fills room, host_token and viewer_key.
		static bool CreateRoom(const std::string& ws_url, HostStatus& out, std::string* error);

		// Creates a room when none is configured, then starts publishing.
		static bool Start(std::string* error);
		static void Stop();

		static HostStatus GetStatus();
		static std::string ServerUrl();
	};
}
