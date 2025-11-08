#!/usr/bin/env python3
"""
Unit tests for beads_mail_adapter.py

Tests cover:
- Enabled mode (server available)
- Disabled mode (server unavailable)
- Graceful degradation (server dies mid-operation)
- Reservation conflicts
- Message sending/receiving
"""

import unittest
import json
import os
from unittest.mock import patch, Mock, MagicMock
from urllib.error import URLError, HTTPError
from io import BytesIO

from beads_mail_adapter import AgentMailAdapter


class TestAgentMailAdapterDisabled(unittest.TestCase):
    """Test adapter when server is unavailable (disabled mode)."""
    
    @patch('beads_mail_adapter.urlopen')
    def test_init_server_unavailable(self, mock_urlopen):
        """Test initialization when server is unreachable."""
        mock_urlopen.side_effect = URLError("Connection refused")
        
        adapter = AgentMailAdapter(
            url="http://localhost:9999",
            token="test-token",
            agent_name="test-agent"
        )
        
        self.assertFalse(adapter.enabled)
        self.assertEqual(adapter.url, "http://localhost:9999")
        self.assertEqual(adapter.agent_name, "test-agent")
    
    @patch('beads_mail_adapter.urlopen')
    def test_operations_no_op_when_disabled(self, mock_urlopen):
        """Test that all operations gracefully no-op when disabled."""
        mock_urlopen.side_effect = URLError("Connection refused")
        
        adapter = AgentMailAdapter()
        self.assertFalse(adapter.enabled)
        
        # All operations should succeed without making requests
        self.assertTrue(adapter.reserve_issue("bd-123"))
        self.assertTrue(adapter.release_issue("bd-123"))
        self.assertTrue(adapter.notify("test", {"foo": "bar"}))
        self.assertEqual(adapter.check_inbox(), [])
        self.assertEqual(adapter.get_reservations(), [])


class TestAgentMailAdapterEnabled(unittest.TestCase):
    """Test adapter when server is available (enabled mode)."""
    
    def _mock_response(self, status_code=200, data=None):
        """Create mock HTTP response."""
        mock_response = MagicMock()
        mock_response.status = status_code
        mock_response.__enter__ = Mock(return_value=mock_response)
        mock_response.__exit__ = Mock(return_value=False)
        
        if data is not None:
            mock_response.read.return_value = json.dumps(data).encode('utf-8')
        else:
            mock_response.read.return_value = b''
        
        return mock_response
    
    @patch('beads_mail_adapter.urlopen')
    def test_init_server_available(self, mock_urlopen):
        """Test initialization when server is healthy."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        
        adapter = AgentMailAdapter(
            url="http://localhost:8765",
            token="test-token",
            agent_name="test-agent"
        )
        
        self.assertTrue(adapter.enabled)
        self.assertEqual(adapter.url, "http://localhost:8765")
    
    @patch('beads_mail_adapter.urlopen')
    def test_reserve_issue_success(self, mock_urlopen):
        """Test successful issue reservation."""
        # Health check
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        # Reservation request
        mock_urlopen.return_value = self._mock_response(201, {"reserved": True})
        result = adapter.reserve_issue("bd-123")
        
        self.assertTrue(result)
        self.assertTrue(adapter.enabled)
    
    @patch('beads_mail_adapter.urlopen')
    def test_reserve_issue_conflict(self, mock_urlopen):
        """Test reservation conflict (issue already reserved)."""
        # Health check
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        # Simulate 409 Conflict
        error_response = HTTPError(
            url="http://test",
            code=409,
            msg="Conflict",
            hdrs={},
            fp=BytesIO(json.dumps({"error": "Already reserved by other-agent"}).encode('utf-8'))
        )
        mock_urlopen.side_effect = error_response
        
        result = adapter.reserve_issue("bd-123")
        
        self.assertFalse(result)
    
    @patch('beads_mail_adapter.urlopen')
    def test_release_issue_success(self, mock_urlopen):
        """Test successful issue release."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        mock_urlopen.return_value = self._mock_response(204)
        result = adapter.release_issue("bd-123")
        
        self.assertTrue(result)
    
    @patch('beads_mail_adapter.urlopen')
    def test_notify_success(self, mock_urlopen):
        """Test sending notification."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        mock_urlopen.return_value = self._mock_response(201, {"sent": True})
        result = adapter.notify("status_changed", {"issue_id": "bd-123", "status": "in_progress"})
        
        self.assertTrue(result)
    
    @patch('beads_mail_adapter.urlopen')
    def test_check_inbox_with_messages(self, mock_urlopen):
        """Test checking inbox with messages."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        messages = [
            {"from": "agent-1", "event": "completed", "data": {"issue_id": "bd-42"}},
            {"from": "agent-2", "event": "started", "data": {"issue_id": "bd-99"}}
        ]
        mock_urlopen.return_value = self._mock_response(200, messages)
        
        result = adapter.check_inbox()
        
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["from"], "agent-1")
    
    @patch('beads_mail_adapter.urlopen')
    def test_check_inbox_empty(self, mock_urlopen):
        """Test checking empty inbox."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        mock_urlopen.return_value = self._mock_response(200, [])
        result = adapter.check_inbox()
        
        self.assertEqual(result, [])
    
    @patch('beads_mail_adapter.urlopen')
    def test_get_reservations(self, mock_urlopen):
        """Test getting all reservations."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(agent_name="test-agent")
        
        reservations = [
            {"issue_id": "bd-123", "agent": "agent-1"},
            {"issue_id": "bd-456", "agent": "agent-2"}
        ]
        mock_urlopen.return_value = self._mock_response(200, reservations)
        
        result = adapter.get_reservations()
        
        self.assertEqual(len(result), 2)


class TestGracefulDegradation(unittest.TestCase):
    """Test graceful degradation when server fails mid-operation."""
    
    def _mock_response(self, status_code=200, data=None):
        """Create mock HTTP response."""
        mock_response = MagicMock()
        mock_response.status = status_code
        mock_response.__enter__ = Mock(return_value=mock_response)
        mock_response.__exit__ = Mock(return_value=False)
        
        if data is not None:
            mock_response.read.return_value = json.dumps(data).encode('utf-8')
        else:
            mock_response.read.return_value = b''
        
        return mock_response
    
    @patch('beads_mail_adapter.urlopen')
    def test_server_dies_mid_operation(self, mock_urlopen):
        """Test that operations gracefully handle server failures."""
        # Initially healthy
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter()
        self.assertTrue(adapter.enabled)
        
        # Server dies during operation
        mock_urlopen.side_effect = URLError("Connection refused")
        
        # Operations should still succeed (graceful degradation)
        self.assertTrue(adapter.reserve_issue("bd-123"))
        self.assertTrue(adapter.release_issue("bd-123"))
        self.assertTrue(adapter.notify("test", {}))
        self.assertEqual(adapter.check_inbox(), [])
    
    @patch('beads_mail_adapter.urlopen')
    def test_network_timeout(self, mock_urlopen):
        """Test handling of network timeouts."""
        mock_urlopen.return_value = self._mock_response(200, {"status": "ok"})
        adapter = AgentMailAdapter(timeout=1)
        
        mock_urlopen.side_effect = URLError("Timeout")
        
        # Should not crash
        self.assertTrue(adapter.reserve_issue("bd-123"))


class TestConfiguration(unittest.TestCase):
    """Test environment variable configuration."""
    
    @patch.dict(os.environ, {
        'AGENT_MAIL_URL': 'http://custom:9000',
        'AGENT_MAIL_TOKEN': 'custom-token',
        'BEADS_AGENT_NAME': 'custom-agent',
        'AGENT_MAIL_TIMEOUT': '10'
    })
    @patch('beads_mail_adapter.urlopen')
    def test_env_var_configuration(self, mock_urlopen):
        """Test configuration from environment variables."""
        mock_urlopen.side_effect = URLError("Not testing connection")
        
        adapter = AgentMailAdapter()
        
        self.assertEqual(adapter.url, "http://custom:9000")
        self.assertEqual(adapter.token, "custom-token")
        self.assertEqual(adapter.agent_name, "custom-agent")
        self.assertEqual(adapter.timeout, 10)
    
    @patch('beads_mail_adapter.urlopen')
    def test_constructor_overrides_env(self, mock_urlopen):
        """Test that constructor args override environment variables."""
        mock_urlopen.side_effect = URLError("Not testing connection")
        
        adapter = AgentMailAdapter(
            url="http://override:8765",
            token="override-token",
            agent_name="override-agent",
            timeout=3
        )
        
        self.assertEqual(adapter.url, "http://override:8765")
        self.assertEqual(adapter.token, "override-token")
        self.assertEqual(adapter.agent_name, "override-agent")
        self.assertEqual(adapter.timeout, 3)
    
    @patch('beads_mail_adapter.urlopen')
    def test_url_trailing_slash_removed(self, mock_urlopen):
        """Test that trailing slashes are removed from URL."""
        mock_urlopen.side_effect = URLError("Not testing connection")
        
        adapter = AgentMailAdapter(url="http://localhost:8765/")
        
        self.assertEqual(adapter.url, "http://localhost:8765")


if __name__ == '__main__':
    unittest.main()
