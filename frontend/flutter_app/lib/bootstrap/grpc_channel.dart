import 'package:grpc/grpc.dart';

/// createGrpcChannel creates a gRPC channel to the backend
ClientChannel createGrpcChannel(String host, int port, String token) {
  final channel = ClientChannel(
    host,
    port: port,
    options: ChannelOptions(credentials: const ChannelCredentials.insecure()),
  );

  // Token would be used for authentication in a real implementation
  // For now, we just set up the channel
  return channel;
}

/// OnseiClient wraps the gRPC client with auth
class OnseiClient {
  final ClientChannel channel;
  final String token;

  OnseiClient({required this.channel, required this.token});

  void close() {
    channel.shutdown();
  }
}
