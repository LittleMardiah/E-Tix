'use client';
import { useRouter } from 'next/navigation';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { useAuthStore } from '@/lib/stores/authStore';

/**
 * Placeholder destination for the post-auth redirect (login / verify-otp).
 * The full customer dashboard is a separate Epic C screen.
 */
export default function CustomerDashboard() {
  const router = useRouter();
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);

  const handleLogout = () => {
    logout();
    router.push('/auth/login');
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <Card className="w-full max-w-md">
        <h1 className="text-2xl font-bold text-gray-900 text-center mb-2">
          Customer Dashboard
        </h1>
        {user && (
          <p className="text-center text-sm text-gray-600 mb-4">
            Signed in as <span className="font-medium">{user.name}</span>{' '}
            ({user.email})
          </p>
        )}
        <p className="text-center text-sm text-gray-600 mb-6">
          Authentication successful. This screen is a placeholder and will be
          implemented in the next Epic C milestone.
        </p>
        <div className="flex gap-3 justify-center">
          <Button variant="outline" onClick={handleLogout}>
            Logout
          </Button>
          <Button href="/" variant="secondary">
            Back to Home
          </Button>
        </div>
      </Card>
    </div>
  );
}
