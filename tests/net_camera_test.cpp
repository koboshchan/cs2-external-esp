// Camera matrix tests. Pure C++20, no Windows headers:
//   clang++ -std=c++20 -O1 -I src tests/net_camera_test.cpp -o /tmp/net_camera_test

#include "net/camera.hpp"

#include <cstdio>
#include <cmath>

namespace
{
	int g_failures = 0;

	void check(bool cond, const char* what)
	{
		if (!cond)
		{
			std::printf("FAIL: %s\n", what);
			g_failures++;
		}
	}

	void check_near(float got, float want, float eps, const char* what)
	{
		if (std::fabs(got - want) > eps)
		{
			std::printf("FAIL: %s (got %.6f want %.6f)\n", what, got, want);
			g_failures++;
		}
	}

	constexpr float kDeg = 0.017453292519943295f;
}

int main()
{
	// --- 1. Looking down +X: forward maps to the screen centre -------------
	{
		float m[16];
		net::BuildViewMatrix({ 0, 0, 0 }, { 0, 0 }, 90.f, 4.f / 3.f, net::kFovHorizontal, m);

		net::Vec2 s;
		check(net::Project(m, { 100, 0, 0 }, 1920, 1080, s), "point in front projects");
		check_near(s.x, 960.f, 0.01f, "forward point at centre x");
		check_near(s.y, 540.f, 0.01f, "forward point at centre y");

		// 45 degrees to the player's right (+Y is left, so -Y is right)
		const float d = 70.710678f;
		check(net::Project(m, { d, -d, 0 }, 1920, 1080, s), "45deg right projects");
		check_near(s.x, 1920.f, 0.5f, "45deg right lands on right edge (fov 90, 4:3)");

		// Half the vertical FOV up: tan(36.87) = 0.75
		check(net::Project(m, { 80, 0, 60 }, 1920, 1080, s), "vfov edge projects");
		check_near(s.y, 0.f, 0.5f, "half vfov up lands on top edge");
	}

	// --- 2. Hor+ at 16:9 widens the horizontal FOV ------------------------
	{
		float m[16];
		net::BuildViewMatrix({ 0, 0, 0 }, { 0, 0 }, 90.f, 16.f / 9.f, net::kFovSourceHorPlus, m);

		check_near(net::MatrixXScale(m), 0.75f, 1e-5f, "hor+ x scale at 16:9");
		check_near(net::MatrixYScale(m), 0.75f * 16.f / 9.f, 1e-5f, "y scale = x scale * aspect");

		// 53.13 degrees right is the new screen edge
		const float a = 53.130102f * kDeg;
		net::Vec3 p{ std::cos(a) * 100.f, -std::sin(a) * 100.f, 0.f };
		net::Vec2 s;
		check(net::Project(m, p, 2560, 1440, s), "hor+ edge projects");
		check_near(s.x, 2560.f, 0.5f, "hor+ 16:9 edge at 53.13deg");
	}

	// --- 3. Translation: projecting eye + forward*N hits the centre -------
	{
		const net::Vec3 eye{ 100.f, 200.f, 50.f };
		float m[16];
		net::BuildViewMatrix(eye, { 0, 0 }, 90.f, 16.f / 9.f, net::kFovSourceHorPlus, m);

		net::Vec2 s;
		check(net::Project(m, { eye.x + 300.f, eye.y, eye.z }, 1920, 1080, s), "eye-relative point projects");
		check_near(s.x, 960.f, 0.01f, "eye-relative centre x");
		check_near(s.y, 540.f, 0.01f, "eye-relative centre y");
	}

	// --- 4. Hand-computed matrix for yaw 90, pitch 0 ----------------------
	{
		float m[16];
		net::BuildViewMatrix({ 10, 20, 30 }, { 0, 90 }, 90.f, 1.f, net::kFovHorizontal, m);

		// fwd = (0,1,0), right = (1,0,0), up = (0,0,1), xs = ys = 1
		check_near(m[0], 1.f, 1e-6f, "m00");
		check_near(m[3], -10.f, 1e-6f, "m03 translation");
		check_near(m[6], 1.f, 1e-6f, "m22 up z");
		check_near(m[7], -30.f, 1e-6f, "m23 translation");
		check_near(m[13], 1.f, 1e-6f, "m31 forward y");
		check_near(m[15], -20.f, 1e-6f, "m33 translation");

		net::Vec2 s;
		check(net::Project(m, { 10, 120, 30 }, 800, 600, s), "hand case projects");
		check_near(s.x, 400.f, 0.01f, "hand case centre x");
		check_near(s.y, 300.f, 0.01f, "hand case centre y");
	}

	// --- 5. FOV mode detection from a real matrix ------------------------
	{
		const float aspects[] = { 16.f / 9.f, 16.f / 10.f, 4.f / 3.f, 21.f / 9.f };
		for (float aspect : aspects)
		{
			for (uint8_t mode : { net::kFovSourceHorPlus, net::kFovHorizontal })
			{
				float m[16];
				net::BuildViewMatrix({ 0, 0, 0 }, { 0, 0 }, 90.f, aspect, mode, m);

				const net::FovDetection d = net::DetectFovMode(90.f, aspect, m);
				if (aspect == 4.f / 3.f)
					check(d.mode == net::kFovSourceHorPlus, "4:3 is ambiguous, defaults to hor+");
				else
					check(d.mode == mode, "fov mode detected");
			}
		}
	}

	// --- 6. Independent basis-based projection cross-check ---------------
	{
		const net::Vec3 eye{ -300.f, 42.f, 17.f };
		const net::Vec2 angles{ 7.5f, -123.f };
		const float fov = 90.f;
		const float aspect = 16.f / 9.f;

		float m[16];
		net::BuildViewMatrix(eye, angles, fov, aspect, net::kFovSourceHorPlus, m);

		const float xs = net::XScaleFromFov(fov, aspect, net::kFovSourceHorPlus);
		const float ys = xs * aspect;

		// Rebuild the basis by hand and project in view space.
		const float pitch = angles.x * kDeg;
		const float yaw = angles.y * kDeg;
		const float cp = std::cos(pitch), sp = std::sin(pitch);
		const float cy = std::cos(yaw), sy = std::sin(yaw);
		const net::Vec3 fwd{ cp * cy, cp * sy, -sp };
		const net::Vec3 right{ sy, -cy, 0.f };
		const net::Vec3 up{ sp * cy, sp * sy, cp };

		const net::Vec3 p{ eye.x + 137.f, eye.y - 411.f, eye.z + 233.f };
		const net::Vec3 d{ p.x - eye.x, p.y - eye.y, p.z - eye.z };

		const float vx = right.x * d.x + right.y * d.y + right.z * d.z;
		const float vy = up.x * d.x + up.y * d.y + up.z * d.z;
		const float vw = fwd.x * d.x + fwd.y * d.y + fwd.z * d.z;

		net::Vec2 s;
		check(net::Project(m, p, 1920, 1080, s), "cross-check projects");

		const float expected_x = 960.f + (xs * vx / vw) * 960.f;
		const float expected_y = 540.f - (ys * vy / vw) * 540.f;

		check_near(s.x, expected_x, 0.05f, "cross-check x");
		check_near(s.y, expected_y, 0.05f, "cross-check y");
	}

	// --- 7. Points behind the camera are rejected ------------------------
	{
		float m[16];
		net::BuildViewMatrix({ 0, 0, 0 }, { 0, 0 }, 90.f, 16.f / 9.f, net::kFovSourceHorPlus, m);
		net::Vec2 s;
		check(!net::Project(m, { -50, 0, 0 }, 1920, 1080, s), "point behind rejected");
	}

	if (g_failures == 0)
		std::printf("camera: all checks passed\n");

	return g_failures == 0 ? 0 : 1;
}
