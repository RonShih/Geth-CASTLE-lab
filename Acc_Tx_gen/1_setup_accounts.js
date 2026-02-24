const { ethers } = require('ethers');
const fs = require('fs');

// 配置參數
const CONFIG = {
    NUM_USERS: 1000,
    FUND_AMOUNT_ETH: 10,
    GAS_LIMIT: 21000,
    ACCOUNTS_FILE: './accounts.json'
};

console.log('🔧 配置:', CONFIG);

class AccountSetup {
    constructor() {
        this.provider = new ethers.JsonRpcProvider('http://127.0.0.1:8545');
        this.users = [];
        this.devAccount = null;
    }

    async initialize() {
        console.log('🚀 初始化環境...');

        // 檢查 Geth 連接
        try {
            const network = await this.provider.getNetwork();
            const blockNumber = await this.provider.getBlockNumber();
            console.log(`✅ 已連接到 Geth (ChainID: ${network.chainId}, 區塊: ${blockNumber})`);
        } catch (error) {
            console.error('❌ 無法連接到 Geth，請確認 Geth 正在運行於 http://127.0.0.1:8545');
            throw error;
        }

        // 獲取 dev account
        const accounts = await this.provider.send('eth_accounts', []);
        this.devAccount = accounts[0];
        console.log(`Dev account: ${this.devAccount}`);
    }

    async generateUsers() {
        console.log(`👥 生成 ${CONFIG.NUM_USERS} 個用戶...`);
        const progressStep = Math.max(1, Math.floor(CONFIG.NUM_USERS / 10));

        for (let i = 0; i < CONFIG.NUM_USERS; i++) {
            const wallet = ethers.Wallet.createRandom();
            this.users.push({
                address: wallet.address,
                privateKey: wallet.privateKey
            });

            if ((i + 1) % progressStep === 0) {
                console.log(`已生成 ${i + 1}/${CONFIG.NUM_USERS} 個用戶...`);
            }
        }

        console.log(`✅ 已生成 ${this.users.length} 個用戶`);
    }

    async fundUsers() {
        console.log('💰 為用戶分配資金...');
        const fundAmount = ethers.parseEther(CONFIG.FUND_AMOUNT_ETH.toString());
        const progressStep = Math.max(1, Math.floor(CONFIG.NUM_USERS / 10));

        for (let i = 0; i < this.users.length; i++) {
            const user = this.users[i];
            try {
                await this.provider.send('eth_sendTransaction', [{
                    from: this.devAccount,
                    to: user.address,
                    value: ethers.toQuantity(fundAmount),
                    gas: ethers.toQuantity(CONFIG.GAS_LIMIT)
                }]);

                if ((i + 1) % progressStep === 0) {
                    console.log(`已資助 ${i + 1}/${CONFIG.NUM_USERS} 用戶`);
                }

                // 每 10 筆交易等待一下
                if (i % 10 === 9) {
                    await new Promise(r => setTimeout(r, 500));
                }

            } catch (error) {
                console.error(`資助用戶 ${i} 失敗:`, error.message);
            }
        }

        // 等待交易確認
        await new Promise(r => setTimeout(r, 3000));
        console.log('✅ 資金分配完成');
    }

    async saveAccounts() {
        console.log(`💾 保存帳號資訊到 ${CONFIG.ACCOUNTS_FILE}...`);

        const data = {
            config: CONFIG,
            devAccount: this.devAccount,
            users: this.users,
            timestamp: new Date().toISOString()
        };

        fs.writeFileSync(CONFIG.ACCOUNTS_FILE, JSON.stringify(data, null, 2));
        console.log(`✅ 已保存 ${this.users.length} 個帳號`);
    }

    async run() {
        try {
            const startTime = Date.now();

            await this.initialize();
            await this.generateUsers();
            await this.fundUsers();
            await this.saveAccounts();

            const totalTime = ((Date.now() - startTime) / 1000).toFixed(1);
            console.log('\n🎉 帳號設置完成！');
            console.log(`總耗時: ${totalTime}s`);
            console.log(`帳號數量: ${this.users.length}`);
            console.log(`帳號文件: ${CONFIG.ACCOUNTS_FILE}`);

        } catch (error) {
            console.error('❌ 設置失敗:', error);
            process.exit(1);
        }
    }
}

// 執行設置
new AccountSetup().run().then(() => process.exit(0)).catch(e => {
    console.error('程式錯誤:', e);
    process.exit(1);
});
