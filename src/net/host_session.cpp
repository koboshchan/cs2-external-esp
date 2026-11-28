#include "net/host_session.hpp"

#include "net/endpoint.hpp"
#include "net/publisher.hpp"

#include "common.hpp"
#include "config/Current.hpp"
#include "updater/http/HttpHelper.hpp"

namespace net
{
	namespace
	{
		HostStatus g_status;
	}

	bool HostSession::CreateRoom(const std::string& ws_url, HostStatus& out, std::string* error)
	{
		const std::string base = HttpBaseFromWsUrl(ws_url);

		json body = json::object();
		json response;

		const int status = HttpHelper::Post(base + "/rooms", body, response);

		if (status != 200 && status != 201)
		{
			if (error)
				*error = "room creation failed (HTTP " + std::to_string(status) + ")";
			return false;
		}

		if (!response.is_object() || !response.contains("room") || !response["room"].is_string() ||
			!response.contains("hostToken") || !response["hostToken"].is_string() ||
			!response.contains("viewerKey") || !response["viewerKey"].is_string())
		{
			if (error) *error = "relay returned invalid room credentials";
			return false;
		}

		HostStatus created;
		created.server = ws_url;
		created.room = response["room"].get<std::string>();
		created.host_token = response["hostToken"].get<std::string>();
		created.viewer_key = response["viewerKey"].get<std::string>();
		if (created.room.empty() || created.host_token.empty() || created.viewer_key.empty())
		{
			if (error) *error = "relay returned an incomplete room";
			return false;
		}
		out = std::move(created);

		// Keep the latest codes visible to the menu even before Start().
		g_status.server = out.server;
		g_status.room = out.room;
		g_status.host_token = out.host_token;
		g_status.viewer_key = out.viewer_key;
		g_status.error.clear();

		LOGF(INFO, "[net] created room {} (viewer key {})", out.room, out.viewer_key);
		return true;
	}

	bool HostSession::Start(std::string* error)
	{
		HostStatus& s = g_status;
		std::string failure;

		s.server = ResolveServerUrl(cfg::remote::url);

		// Reuse an explicitly configured room, otherwise ask the relay for one.
		if (cfg::remote::room.empty() || cfg::remote::token.empty())
		{
			if (!CreateRoom(s.server, s, &failure))
			{
				s.error = failure;
				if (error) *error = failure;
				return false;
			}
		}
		else
		{
			s.room = cfg::remote::room;
			s.host_token = cfg::remote::token;
			s.viewer_key = cfg::remote::viewer_key;
			s.server = ResolveServerUrl(cfg::remote::url);
		}

		cfg::remote::url = s.server;
		cfg::remote::room = s.room;
		cfg::remote::token = s.host_token;
		cfg::remote::viewer_key = s.viewer_key;

		if (!Publisher::Start())
		{
			s.error = "publisher could not start";
			if (error) *error = s.error;
			return false;
		}

		s.active = true;
		s.error.clear();
		return true;
	}

	void HostSession::Stop()
	{
		Publisher::Stop();
		g_status.active = false;
		g_status.connected = false;
	}

	HostStatus HostSession::GetStatus()
	{
		HostStatus status = g_status;

		const PublisherStats stats = Publisher::GetStats();
		status.active = stats.running;
		status.connected = stats.connected;
		status.frames_sent = stats.frames_sent;
		status.bytes_sent = stats.bytes_sent;
		status.dropped = stats.dropped;
		status.viewers = stats.viewers;

		if (stats.running && !stats.last_message.empty())
			status.error = stats.last_message;

		return status;
	}

	std::string HostSession::ServerUrl()
	{
		return ResolveServerUrl(cfg::remote::url);
	}
}
